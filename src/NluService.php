<?php
declare(strict_types=1);

namespace Atlas;

use PDO;
use RuntimeException;

class NluService
{
    private string $apiKey;
    private string $model;
    private PDO $pdo;

    public function __construct(?string $apiKey = null, ?string $model = null, ?PDO $pdo = null)
    {
        $this->apiKey = $apiKey ?: (getenv('NVIDIA_API_KEY') ?: '');
        $this->model = $model ?: (getenv('NVIDIA_MODEL') ?: 'meta/llama-3.1-70b-instruct');
        $this->pdo = $pdo ?: Database::getConnection();

        if (empty($this->apiKey)) {
            throw new RuntimeException("NVIDIA_API_KEY não configurada.");
        }
    }

    public function parseIntent(string $text, string $currentDatetime): ?array
    {
        $systemPrompt = <<<PROMPT
Você é o ATLAS: assistente pessoal socrático, combativo, direto e hiper-racional.
Horário atual: {$currentDatetime}.

PERSONALIDADE:
- Não seja capacho nem dê respostas genéricas.
- Use o Método Socrático: questione premissas frágeis, custos de oportunidade e autoengano.
- Se o usuário trouxer uma ideia, registre-a, mas devolva uma pergunta cortante que coloque à prova a viabilidade real ou o motivo da escolha.

Classifique a intenção em: "ideia", "evento", "objetivo" ou "conversa".

Retorne EXCLUSIVAMENTE um JSON sem nenhum texto fora:

Para ideias / pensamentos:
{
  "tipo": "ideia",
  "titulo": "Resumo de 3 a 5 palavras",
  "conteudo": "A ideia expressa",
  "pergunta_socratica": "Pergunta afiada confrontando a lógica ou a motivação da ideia"
}

Para compromissos:
{
  "tipo": "evento",
  "descricao": "Nome do compromisso",
  "horario_oficial": "YYYY-MM-DD HH:MM:SS",
  "estimativa_deslocamento_min": 25,
  "estimativa_preparo_min": 35
}

Para metas:
{
  "tipo": "objetivo",
  "titulo": "Meta declarada",
  "prazo": "YYYY-MM-DD HH:MM:SS" (ou null)
}

Para conversa / debate:
{
  "tipo": "conversa",
  "resposta": "Sua resposta socrática, incisiva e concisa"
}
PROMPT;

        // Recupera as últimas 6 mensagens do SQLite para dar contexto contínuo à NVIDIA API
        $stmt = $this->pdo->query("SELECT role, content FROM historico_chat ORDER BY id DESC LIMIT 6");
        $history = array_reverse($stmt->fetchAll(PDO::FETCH_ASSOC));

        $messages = [['role' => 'system', 'content' => $systemPrompt]];
        foreach ($history as $h) {
            $messages[] = ['role' => $h['role'], 'content' => $h['content']];
        }
        $messages[] = ['role' => 'user', 'content' => $text];

        $payload = json_encode([
            'model' => $this->model,
            'messages' => $messages,
            'temperature' => 0.3,
            'max_tokens' => 350,
        ], JSON_THROW_ON_ERROR);

        $ch = curl_init('https://integrate.api.nvidia.com/v1/chat/completions');
        curl_setopt_array($ch, [
            CURLOPT_RETURNTRANSFER => true,
            CURLOPT_POST => true,
            CURLOPT_POSTFIELDS => $payload,
            CURLOPT_HTTPHEADER => [
                'Content-Type: application/json',
                'Authorization: Bearer ' . $this->apiKey,
            ],
            CURLOPT_TIMEOUT => 15,
        ]);

        $response = curl_exec($ch);
        $err = curl_error($ch);
        curl_close($ch);

        if ($err || !$response) {
            return null;
        }

        $resData = json_decode($response, true);
        $content = $resData['choices'][0]['message']['content'] ?? '';

        if (preg_match('/\{.*\}/s', $content, $matches)) {
            return json_decode($matches[0], true);
        }

        return null;
    }

    public function recordHistory(string $role, string $content): void
    {
        $stmt = $this->pdo->prepare("INSERT INTO historico_chat (role, content) VALUES (:r, :c)");
        $stmt->execute([':r' => $role, ':c' => $content]);
    }
}
