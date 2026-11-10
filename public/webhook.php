<?php
declare(strict_types=1);

require_once __DIR__ . '/../src/Database.php';
require_once __DIR__ . '/../src/NluService.php';
require_once __DIR__ . '/../src/SttService.php';
require_once __DIR__ . '/../src/TelegramService.php';
require_once __DIR__ . '/../src/TimeBufferCalculator.php';

use Atlas\Database;
use Atlas\NluService;
use Atlas\SttService;
use Atlas\TelegramService;
use Atlas\TimeBufferCalculator;

$envFile = __DIR__ . '/../.env';
if (file_exists($envFile)) {
    foreach (file($envFile, FILE_IGNORE_NEW_LINES | FILE_SKIP_EMPTY_LINES) as $line) {
        if (str_starts_with(trim($line), '#') || !str_contains($line, '=')) continue;
        [$k, $v] = explode('=', $line, 2);
        putenv(trim($k) . '=' . trim($v));
    }
}

date_default_timezone_set(getenv('TIMEZONE') ?: 'America/Sao_Paulo');

// 1. Segurança do Webhook
$expectedSecret = getenv('TELEGRAM_SECRET_TOKEN');
$receivedSecret = $_SERVER['HTTP_X_TELEGRAM_BOT_API_SECRET_TOKEN'] ?? '';
if ($expectedSecret && !hash_equals($expectedSecret, $receivedSecret)) {
    http_response_code(403);
    exit('Forbidden');
}

$raw = file_get_contents('php://input');
$update = json_decode($raw, true);

if (!isset($update['message'])) {
    http_response_code(200);
    exit('OK');
}

$message = $update['message'];
$chatId = $message['chat']['id'];
$telegram = new TelegramService();

// 2. Extração de Áudio ou Texto
$inputText = '';
if (isset($message['text'])) {
    $inputText = trim($message['text']);
} elseif (isset($message['voice']['file_id'])) {
    $tempOgg = tempnam(sys_get_temp_dir(), 'voice_') . '.oga';
    if ($telegram->downloadFile($message['voice']['file_id'], $tempOgg)) {
        try {
            $stt = new SttService();
            $inputText = $stt->transcribe($tempOgg);
        } catch (\Throwable $e) {
            $telegram->sendMessage($chatId, "⚠️ Erro na transcrição de voz: " . $e->getMessage());
        } finally {
            if (file_exists($tempOgg)) unlink($tempOgg);
        }
    }
}

if (empty($inputText)) {
    http_response_code(200);
    exit('OK');
}

// 3. IA com Contexto Contínuo e Personalidade Socrática
try {
    $nlu = new NluService();
    $now = date('Y-m-d H:i:s');
    $intent = $nlu->parseIntent($inputText, $now);

    // Registra entrada do usuário no histórico
    $nlu->recordHistory('user', $inputText);

    if (!$intent) {
        $msg = "Você soltou uma frase desconexa. O que exatamente você quer decidir ou registrar com isso?";
        $telegram->sendMessage($chatId, $msg);
        $nlu->recordHistory('assistant', $msg);
        exit('OK');
    }

    $pdo = Database::getConnection();
    $reply = '';

    if ($intent['tipo'] === 'ideia') {
        $stmt = $pdo->prepare("
            INSERT INTO ideias (titulo, conteudo, questionamento_socratico) 
            VALUES (:t, :c, :q)
        ");
        $stmt->execute([
            ':t' => $intent['titulo'],
            ':c' => $intent['conteudo'],
            ':q' => $intent['pergunta_socratica'],
        ]);

        $reply = "💡 *Ideia Anotada:* {$intent['titulo']}\n\n"
               . "⚔️ *Desafio:* {$intent['pergunta_socratica']}";

    } elseif ($intent['tipo'] === 'evento') {
        $horarioOficial = new DateTimeImmutable($intent['horario_oficial']);
        $deslocamento = (int) ($intent['estimativa_deslocamento_min'] ?? 25);
        $preparo = (int) ($intent['estimativa_preparo_min'] ?? 35);

        $buffers = TimeBufferCalculator::calculate($horarioOficial, $deslocamento, $preparo);

        $stmt = $pdo->prepare("
            INSERT INTO eventos (descricao, horario_oficial, horario_buffer_1, horario_buffer_2)
            VALUES (:desc, :oficial, :b1, :b2)
        ");
        $stmt->execute([
            ':desc' => $intent['descricao'],
            ':oficial' => $intent['horario_oficial'],
            ':b1' => $buffers['horario_buffer_1'],
            ':b2' => $buffers['horario_buffer_2'],
        ]);

        $reply = "🎯 *Compromisso Agendado (+15% margem)*\n\n"
               . "📌 *Evento:* {$intent['descricao']}\n"
               . "⏰ *Oficial:* {$intent['horario_oficial']}\n"
               . "🛁 *Preparo:* {$buffers['horario_buffer_1']}\n"
               . "🚗 *Saída:* {$buffers['horario_buffer_2']}\n"
               . "🛡️ *Margem:* +{$buffers['margem_minutos']} min extras";

    } elseif ($intent['tipo'] === 'objetivo') {
        $stmt = $pdo->prepare("INSERT INTO objetivos (titulo, prazo) VALUES (:titulo, :prazo)");
        $stmt->execute([
            ':titulo' => $intent['titulo'],
            ':prazo' => $intent['prazo'] ?? null,
        ]);

        $reply = "🎯 *Meta Registrada:* {$intent['titulo']}" . ($intent['prazo'] ? " (Prazo: {$intent['prazo']})" : "");

    } else {
        $reply = $intent['resposta'] ?? "Qual é a sua conclusão sobre isso?";
    }

    $telegram->sendMessage($chatId, $reply);
    $nlu->recordHistory('assistant', $reply);

} catch (\Throwable $e) {
    error_log("Erro no webhook: " . $e->getMessage());
    $telegram->sendMessage($chatId, "❌ Falha no processamento: " . $e->getMessage());
}

http_response_code(200);
echo 'OK';
