<?php
declare(strict_types=1);

require_once __DIR__ . '/../src/TelegramService.php';

use Atlas\TelegramService;

$envFile = __DIR__ . '/../.env';
if (file_exists($envFile)) {
    foreach (file($envFile, FILE_IGNORE_NEW_LINES | FILE_SKIP_EMPTY_LINES) as $line) {
        if (str_starts_with(trim($line), '#') || !str_contains($line, '=')) continue;
        [$k, $v] = explode('=', $line, 2);
        putenv(trim($k) . '=' . trim($v));
    }
}

$dbPath = getenv('DB_PATH') ?: __DIR__ . '/../data/atlas.sqlite';
$chatId = getenv('TELEGRAM_CHAT_ID');

if (!file_exists($dbPath) || !$chatId) {
    exit("Banco não encontrado ou TELEGRAM_CHAT_ID ausente.\n");
}

$telegram = new TelegramService();
$caption = "📦 Backup Automático Atlas (" . date('d/m/Y H:i') . ")";
$telegram->sendDocument($chatId, $dbPath, $caption);
