<?php
declare(strict_types=1);

require_once __DIR__ . '/../src/Database.php';
require_once __DIR__ . '/../src/TelegramService.php';

use Atlas\Database;
use Atlas\TelegramService;

$envFile = __DIR__ . '/../.env';
if (file_exists($envFile)) {
    foreach (file($envFile, FILE_IGNORE_NEW_LINES | FILE_SKIP_EMPTY_LINES) as $line) {
        if (str_starts_with(trim($line), '#') || !str_contains($line, '=')) continue;
        [$k, $v] = explode('=', $line, 2);
        putenv(trim($k) . '=' . trim($v));
    }
}

date_default_timezone_set(getenv('TIMEZONE') ?: 'America/Sao_Paulo');
$chatId = getenv('TELEGRAM_CHAT_ID');

if (!$chatId) {
    exit("TELEGRAM_CHAT_ID não configurado.\n");
}

$pdo = Database::getConnection();
$telegram = new TelegramService();
$now = date('Y-m-d H:i:s');

// 1. Checa alertas de Buffer 1 (Preparo/Banho)
$stmt1 = $pdo->prepare("
    SELECT id, descricao, horario_oficial, horario_buffer_1 
    FROM eventos 
    WHERE notificado_buffer_1 = 0 
      AND horario_buffer_1 IS NOT NULL 
      AND horario_buffer_1 <= :now
");
$stmt1->execute([':now' => $now]);
$eventos1 = $stmt1->fetchAll();

foreach ($eventos1 as $ev) {
    $msg = "🛁 *Alerta de Preparo!*\n\nHora de começar a se arrumar para:\n"
         . "👉 *{$ev['descricao']}* às *{$ev['horario_oficial']}*.";
    if ($telegram->sendMessage($chatId, $msg)) {
        $pdo->prepare("UPDATE eventos SET notificado_buffer_1 = 1 WHERE id = :id")->execute([':id' => $ev['id']]);
    }
}

// 2. Checa alertas de Buffer 2 (Saída de casa)
$stmt2 = $pdo->prepare("
    SELECT id, descricao, horario_oficial, horario_buffer_2 
    FROM eventos 
    WHERE notificado_buffer_2 = 0 
      AND horario_buffer_2 IS NOT NULL 
      AND horario_buffer_2 <= :now
");
$stmt2->execute([':now' => $now]);
$eventos2 = $stmt2->fetchAll();

foreach ($eventos2 as $ev) {
    $msg = "🚗 *Alerta de Saída!*\n\nHora de sair de casa para:\n"
         . "👉 *{$ev['descricao']}* marcado para *{$ev['horario_oficial']}*.";
    if ($telegram->sendMessage($chatId, $msg)) {
        $pdo->prepare("UPDATE eventos SET notificado_buffer_2 = 1 WHERE id = :id")->execute([':id' => $ev['id']]);
    }
}
