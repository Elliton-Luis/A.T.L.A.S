<?php
declare(strict_types=1);

namespace Atlas;

use RuntimeException;

class SttService
{
    private string $groqApiKey;

    public function __construct(?string $apiKey = null)
    {
        $this->groqApiKey = $apiKey ?: (getenv('GROQ_API_KEY') ?: '');
        if (empty($this->groqApiKey)) {
            throw new RuntimeException("GROQ_API_KEY não configurada para STT.");
        }
    }

    public function transcribe(string $audioFilePath): string
    {
        if (!file_exists($audioFilePath)) {
            throw new RuntimeException("Arquivo de áudio não encontrado: {$audioFilePath}");
        }

        $url = 'https://api.groq.com/openai/v1/audio/transcriptions';
        $ch = curl_init($url);

        $cfile = new \CURLFile($audioFilePath, 'audio/ogg', 'voice.oga');
        $fields = [
            'file' => $cfile,
            'model' => 'whisper-large-v3',
            'language' => 'pt',
            'response_format' => 'json',
        ];

        curl_setopt_array($ch, [
            CURLOPT_RETURNTRANSFER => true,
            CURLOPT_POST => true,
            CURLOPT_POSTFIELDS => $fields,
            CURLOPT_HTTPHEADER => [
                'Authorization: Bearer ' . $this->groqApiKey,
            ],
            CURLOPT_TIMEOUT => 20,
        ]);

        $res = curl_exec($ch);
        $err = curl_error($ch);
        curl_close($ch);

        if ($err || !$res) {
            throw new RuntimeException("Falha no STT: {$err}");
        }

        $data = json_decode($res, true);
        return trim($data['text'] ?? '');
    }
}
