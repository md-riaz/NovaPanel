<?php

namespace App\Support;

class SystemStatusService
{
    /**
     * @return array<string, mixed>
     */
    public function snapshot(): array
    {
        $services = [
            $this->inspectService('nginx', 'Nginx', ['nginx']),
            $this->inspectService('php_fpm', 'PHP-FPM', $this->phpFpmUnits()),
            $this->inspectService('mysql', 'MySQL', ['mysql', 'mariadb']),
            $this->inspectService('bind', 'BIND', ['bind9', 'named']),
            $this->inspectService('ftp', 'FTP', ['pure-ftpd', 'vsftpd', 'proftpd']),
        ];

        $healthyServices = count(array_filter($services, static fn (array $service): bool => $service['healthy']));
        $installedServices = count(array_filter($services, static fn (array $service): bool => $service['installed']));

        return [
            'services' => $services,
            'disk' => $this->diskUsage('/'),
            'memory' => $this->memoryUsage(),
            'load' => $this->loadAverage(),
            'health' => [
                'healthy' => $healthyServices,
                'installed' => $installedServices,
                'total' => count($services),
                'summary' => $installedServices === 0
                    ? 'Service discovery unavailable'
                    : sprintf('%d of %d installed services healthy', $healthyServices, $installedServices),
            ],
        ];
    }

    /**
     * @param array<int, string> $units
     * @return array<string, mixed>
     */
    private function inspectService(string $key, string $label, array $units): array
    {
        if (!$this->commandExists('systemctl')) {
            return [
                'key' => $key,
                'label' => $label,
                'unit' => null,
                'installed' => false,
                'healthy' => false,
                'state' => 'unknown',
                'badge' => 'secondary',
                'details' => 'systemctl is not available on this host.',
            ];
        }

        foreach ($units as $unit) {
            $loadState = trim($this->runCommand(sprintf('systemctl show %s --property=LoadState --value 2>/dev/null', escapeshellarg($unit))));

            if ($loadState === '' || $loadState === 'not-found') {
                continue;
            }

            $activeState = trim($this->runCommand(sprintf('systemctl show %s --property=ActiveState --value 2>/dev/null', escapeshellarg($unit))));
            $subState = trim($this->runCommand(sprintf('systemctl show %s --property=SubState --value 2>/dev/null', escapeshellarg($unit))));

            $healthy = $activeState === 'active';

            return [
                'key' => $key,
                'label' => $label,
                'unit' => $unit,
                'installed' => true,
                'healthy' => $healthy,
                'state' => $activeState !== '' ? $activeState : 'unknown',
                'badge' => $healthy ? 'success' : 'warning',
                'details' => $subState !== ''
                    ? sprintf('Unit %s is %s (%s).', $unit, $activeState ?: 'unknown', $subState)
                    : sprintf('Unit %s is %s.', $unit, $activeState ?: 'unknown'),
            ];
        }

        return [
            'key' => $key,
            'label' => $label,
            'unit' => null,
            'installed' => false,
            'healthy' => false,
            'state' => 'not installed',
            'badge' => 'secondary',
            'details' => sprintf('No matching service unit found for %s.', $label),
        ];
    }

    /**
     * @return array<int, string>
     */
    private function phpFpmUnits(): array
    {
        $units = [];

        foreach (glob('/etc/php/*/fpm') ?: [] as $path) {
            $version = basename(dirname($path));
            $units[] = sprintf('php%s-fpm', $version);
        }

        if ($units === []) {
            $units = ['php8.3-fpm', 'php8.2-fpm', 'php8.1-fpm', 'php8.0-fpm', 'php7.4-fpm'];
        }

        return array_values(array_unique($units));
    }

    /**
     * @return array<string, mixed>
     */
    private function diskUsage(string $path): array
    {
        $total = @disk_total_space($path) ?: 0;
        $free = @disk_free_space($path) ?: 0;
        $used = max(0, $total - $free);
        $percent = $total > 0 ? round(($used / $total) * 100, 1) : null;

        return [
            'path' => $path,
            'used' => $used,
            'free' => $free,
            'total' => $total,
            'used_human' => $this->formatBytes($used),
            'free_human' => $this->formatBytes($free),
            'total_human' => $this->formatBytes($total),
            'percent' => $percent,
        ];
    }

    /**
     * @return array<string, mixed>
     */
    private function memoryUsage(): array
    {
        $memInfo = @file('/proc/meminfo', FILE_IGNORE_NEW_LINES | FILE_SKIP_EMPTY_LINES) ?: [];
        $values = [];

        foreach ($memInfo as $line) {
            if (preg_match('/^(\w+):\s+(\d+)/', $line, $matches)) {
                $values[$matches[1]] = (int) $matches[2] * 1024;
            }
        }

        $total = $values['MemTotal'] ?? 0;
        $available = $values['MemAvailable'] ?? ($values['MemFree'] ?? 0);
        $used = max(0, $total - $available);
        $percent = $total > 0 ? round(($used / $total) * 100, 1) : null;

        return [
            'used' => $used,
            'available' => $available,
            'total' => $total,
            'used_human' => $this->formatBytes($used),
            'available_human' => $this->formatBytes($available),
            'total_human' => $this->formatBytes($total),
            'percent' => $percent,
        ];
    }

    /**
     * @return array<string, mixed>
     */
    private function loadAverage(): array
    {
        $load = sys_getloadavg();

        return [
            'one' => $load[0] ?? 0.0,
            'five' => $load[1] ?? 0.0,
            'fifteen' => $load[2] ?? 0.0,
            'cpu_cores' => $this->cpuCores(),
        ];
    }

    private function cpuCores(): int
    {
        $cpuInfo = @file('/proc/cpuinfo', FILE_IGNORE_NEW_LINES | FILE_SKIP_EMPTY_LINES) ?: [];
        $cores = count(array_filter($cpuInfo, static fn (string $line): bool => str_starts_with($line, 'processor')));

        return max(1, $cores);
    }

    private function commandExists(string $command): bool
    {
        $result = trim($this->runCommand(sprintf('command -v %s 2>/dev/null', escapeshellarg($command))));
        return $result !== '';
    }

    private function runCommand(string $command): string
    {
        return (string) shell_exec($command);
    }

    private function formatBytes(int|float $bytes): string
    {
        if ($bytes <= 0) {
            return '0 B';
        }

        $units = ['B', 'KB', 'MB', 'GB', 'TB'];
        $power = (int) floor(log($bytes, 1024));
        $power = min($power, count($units) - 1);
        $value = $bytes / (1024 ** $power);

        return sprintf('%.1f %s', $value, $units[$power]);
    }
}
