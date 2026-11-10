<?php
declare(strict_types=1);

namespace Atlas;

use DateTimeImmutable;

class TimeBufferCalculator
{
    private const MARGEM_ATRASO_PADRAO = 0.15; // 15% de margem determinística

    /**
     * Calcula os buffers de alerta determinísticos:
     * - buffer_saida: tempo de deslocamento + 15% de margem
     * - buffer_preparo: tempo de se arrumar + buffer_saida
     */
    public static function calculate(
        DateTimeImmutable $horarioOficial,
        int $deslocamentoMin = 25,
        int $preparoMin = 35,
        float $fatorAtraso = self::MARGEM_ATRASO_PADRAO
    ): array {
        // Aplica 15% sobre o tempo total estimado de logística
        $deslocamentoComMargem = (int) ceil($deslocamentoMin * (1 + $fatorAtraso));
        $preparoComMargem = (int) ceil($preparoMin * (1 + $fatorAtraso));

        $minutosAteSaida = $deslocamentoComMargem;
        $minutosAtePreparo = $deslocamentoComMargem + $preparoComMargem;

        $bufferSaida = $horarioOficial->modify("-{$minutosAteSaida} minutes");
        $bufferPreparo = $horarioOficial->modify("-{$minutosAtePreparo} minutes");

        return [
            'horario_buffer_1' => $bufferPreparo->format('Y-m-d H:i:s'), // Hora de se arrumar
            'horario_buffer_2' => $bufferSaida->format('Y-m-d H:i:s'),   // Hora de sair
            'margem_minutos' => ($deslocamentoComMargem - $deslocamentoMin) + ($preparoComMargem - $preparoMin),
        ];
    }
}
