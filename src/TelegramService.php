<?php
declare(strict_types=1);

namespace Atlas;

class TelegramService
{
    private string $botToken;

    public function __construct(?string $botToken = null)
    {
        $this->botToken = $botToken ?: (getenv('TELEGRAM_BOT_TOKEN') ?: '');
    }

    public function sendMessage(string|int $chatId, string $text): bool
    {
        $url = "https://api.telegram.org/bot{$this->botToken}/sendMessage";
        $ch = curl_init($url);
        curl_setopt_array($ch, [
            CURLOPT_RETURNTRANSFER => true,
            CURLOPT_POST => true,
            CURLOPT_POSTFIELDS => json_encode([
                'chat_id' => $chatId,
                'text' => $text,
                'parse_mode' => 'Markdown',
            ]),
            CURLOPT_HTTPHEADER => ['Content-Type: application/json'],
            CURLOPT_TIMEOUT => 10,
        ]);

        $res = curl_exec($ch);
        $code = curl_getinfo($ch, CURLINFO_HTTP_CODE);
        curl_close($ch);

        return $code === 200;
    }

    public function downloadFile(string $fileId, string $destinationPath): bool
    {
        // 1. Obter caminho do arquivo via API
        $url = "https://api.telegram.org/bot{$this->botToken}/getFile?file_id={$fileId}";
        $res = file_get_contents($url);
        if (!$res) return false;

        $data = json_decode($res, true);
        $filePath = $data['result']['file_path'] ?? null;
        if (!$filePath) return false;

        // 2. Download do binário
        $fileUrl = "https://api.telegram.org/file/bot{$this->botToken}/{$filePath}";
        $bin = file_get_contents($fileUrl);
        if (!$bin) return false;

        return file_put_contents($destinationPath, $bin) !== false;
    }

    public function sendDocument(string|int $chatId, string $filePath, string $caption = ''): bool
    {
        if (!file_exists($filePath)) {
            return false;
        }

        $url = "https://api.telegram.org/bot{$this->botToken}/sendDocument";
        $ch = curl_init($url);
        curl_setopt_array($ch, [
            CURLOPT_RETURNTRANSFER => true,
            CURLOPT_POST => true,
            CURLOPT_POSTFIELDS => [
                'chat_id' => $chatId,
                'document' => new \CURLFile($filePath),
                'caption' => $caption,
            ],
            CURLOPT_TIMEOUT => 30,
        ]);

        $res = curl_exec($ch);
        $code = curl_getinfo($ch, CURLINFO_HTTP_CODE);
        curl_close($ch);

        return $code === 200;
    }
}
