<?php
declare(strict_types=1);

namespace Atlas;

use PDO;

class Database
{
    private static ?PDO $instance = null;

    public static function getConnection(): PDO
    {
        if (self::$instance === null) {
            $dbPath = getenv('DB_PATH') ?: __DIR__ . '/../data/atlas.sqlite';
            $dir = dirname($dbPath);
            if (!is_dir($dir)) {
                mkdir($dir, 0755, true);
            }

            self::$instance = new PDO("sqlite:" . $dbPath, null, null, [
                PDO::ATTR_ERRMODE => PDO::ERRMODE_EXCEPTION,
                PDO::ATTR_DEFAULT_FETCH_MODE => PDO::FETCH_ASSOC,
            ]);

            self::initSchema(self::$instance);
        }

        return self::$instance;
    }

    private static function initSchema(PDO $pdo): void
    {
        $pdo->exec("
            -- Compromissos e Tarefas
            CREATE TABLE IF NOT EXISTS eventos (
                id INTEGER PRIMARY KEY AUTOINCREMENT,
                descricao TEXT NOT NULL,
                horario_oficial DATETIME NOT NULL,
                horario_buffer_1 DATETIME,
                horario_buffer_2 DATETIME,
                notificado_buffer_1 INTEGER DEFAULT 0,
                notificado_buffer_2 INTEGER DEFAULT 0,
                status TEXT CHECK(status IN ('pendente', 'em_andamento', 'concluido', 'esquecido')) DEFAULT 'pendente',
                criado_em DATETIME DEFAULT CURRENT_TIMESTAMP
            );

            -- Objetivos e Metas
            CREATE TABLE IF NOT EXISTS objetivos (
                id INTEGER PRIMARY KEY AUTOINCREMENT,
                titulo TEXT NOT NULL,
                prazo DATETIME,
                progresso_pct INTEGER DEFAULT 0,
                status TEXT CHECK(status IN ('ativo', 'concluido', 'pausado')) DEFAULT 'ativo',
                criado_em DATETIME DEFAULT CURRENT_TIMESTAMP
            );

            -- Anotações de Ideias e Pensamentos
            CREATE TABLE IF NOT EXISTS ideias (
                id INTEGER PRIMARY KEY AUTOINCREMENT,
                titulo TEXT NOT NULL,
                conteudo TEXT NOT NULL,
                questionamento_socratico TEXT,
                status TEXT CHECK(status IN ('bruta', 'refinada', 'descartada', 'em_execucao')) DEFAULT 'bruta',
                criado_em DATETIME DEFAULT CURRENT_TIMESTAMP
            );

            -- Histórico de Mensagens Recentes para Contexto da NVIDIA
            CREATE TABLE IF NOT EXISTS historico_chat (
                id INTEGER PRIMARY KEY AUTOINCREMENT,
                role TEXT CHECK(role IN ('user', 'assistant')) NOT NULL,
                content TEXT NOT NULL,
                criado_em DATETIME DEFAULT CURRENT_TIMESTAMP
            );

            CREATE INDEX IF NOT EXISTS idx_eventos_notificacao ON eventos(
                notificado_buffer_1,
                notificado_buffer_2,
                horario_buffer_1,
                horario_buffer_2
            );
        ");
    }
}
