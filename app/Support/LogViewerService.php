<?php

namespace App\Support;

class LogViewerService
{
    private string $projectRoot;

    public function __construct(?string $projectRoot = null)
    {
        $this->projectRoot = $projectRoot ?? dirname(__DIR__, 2);
    }

    /**
     * @return array<int, array<string, string|null>>
     */
    public function sources(): array
    {
        $phpFpmPath = $this->detectPhpFpmLogPath();
        $appLogPath = $this->firstExistingPath([
            $this->projectRoot . '/storage/logs/app.log',
            $this->projectRoot . '/storage/logs/shell.log',
        ]);

        return [
            [
                'key' => 'nginx_access',
                'label' => 'Nginx access',
                'description' => 'Recent HTTP access activity handled by Nginx.',
                'path' => '/var/log/nginx/access.log',
            ],
            [
                'key' => 'nginx_error',
                'label' => 'Nginx error',
                'description' => 'Nginx upstream, routing, and configuration errors.',
                'path' => '/var/log/nginx/error.log',
            ],
            [
                'key' => 'php_fpm',
                'label' => 'PHP-FPM',
                'description' => 'PHP-FPM worker, pool, and runtime events.',
                'path' => $phpFpmPath,
            ],
            [
                'key' => 'panel_audit',
                'label' => 'Panel audit',
                'description' => 'Audit history for panel actions and auth events.',
                'path' => $this->projectRoot . '/storage/logs/audit.log',
            ],
            [
                'key' => 'panel_app',
                'label' => 'Panel application',
                'description' => 'Operational log for shell and panel internals.',
                'path' => $appLogPath,
            ],
        ];
    }

    /**
     * @return array<string, mixed>
     */
    public function read(string $sourceKey, int $lines = 200): array
    {
        $source = $this->findSource($sourceKey);
        $path = $source['path'];
        $lines = max(1, min(500, $lines));

        if ($path === null) {
            return [
                'source' => $source,
                'available' => false,
                'content' => sprintf('%s is not available on this host yet.', $source['label']),
            ];
        }

        if (!file_exists($path)) {
            return [
                'source' => $source,
                'available' => false,
                'content' => sprintf('Log file not found: %s', $path),
            ];
        }

        if (!is_readable($path)) {
            return [
                'source' => $source,
                'available' => false,
                'content' => sprintf('Log file is not readable by the panel process: %s', $path),
            ];
        }

        try {
            clearstatcache(true, $path);

            if (!file_exists($path) || !is_readable($path)) {
                return [
                    'source' => $source,
                    'available' => false,
                    'content' => sprintf('Log file not available: %s', $path),
                ];
            }

            $content = implode("\n", $this->tailFile($path, $lines));

            clearstatcache(true, $path);
            $updatedAt = @filemtime($path);
            $size = @filesize($path);
        } catch (\Throwable) {
            return [
                'source' => $source,
                'available' => false,
                'content' => sprintf('Log file not available: %s', $path),
            ];
        }

        return [
            'source' => $source,
            'available' => true,
            'content' => $content !== '' ? $content : 'Log file is empty.',
            'updated_at' => $updatedAt !== false ? date('Y-m-d H:i:s', (int) $updatedAt) : 'Unavailable',
            'size_human' => $size !== false ? $this->formatBytes((int) $size) : 'Unavailable',
        ];
    }

    /**
     * @return array<string, string|null>
     */
    public function defaultSource(): array
    {
        return $this->sources()[0];
    }

    /**
     * @return array<string, string|null>
     */
    private function findSource(string $sourceKey): array
    {
        foreach ($this->sources() as $source) {
            if ($source['key'] === $sourceKey) {
                return $source;
            }
        }

        return $this->defaultSource();
    }

    private function detectPhpFpmLogPath(): ?string
    {
        $paths = array_merge(
            glob('/var/log/php*-fpm.log') ?: [],
            glob('/var/log/php/*fpm*.log') ?: [],
            glob('/var/log/php*/fpm*.log') ?: []
        );

        return $this->firstExistingPath($paths);
    }

    /**
     * @param array<int, string> $paths
     */
    private function firstExistingPath(array $paths): ?string
    {
        foreach ($paths as $path) {
            if ($path !== '' && file_exists($path)) {
                return $path;
            }
        }

        return null;
    }

    /**
     * @return array<int, string>
     */
    private function tailFile(string $path, int $lines): array
    {
        $handle = @fopen($path, 'rb');
        if ($handle === false) {
            throw new \RuntimeException(sprintf('Unable to open log file: %s', $path));
        }

        $buffer = '';
        $position = @filesize($path);
        $chunkSize = 4096;

        if ($position === false) {
            fclose($handle);
            throw new \RuntimeException(sprintf('Unable to stat log file: %s', $path));
        }

        while ($position > 0 && substr_count($buffer, "\n") <= $lines) {
            $readSize = min($chunkSize, $position);
            $position -= $readSize;

            if (fseek($handle, $position, SEEK_SET) !== 0) {
                fclose($handle);
                throw new \RuntimeException(sprintf('Unable to seek log file: %s', $path));
            }

            $chunk = fread($handle, $readSize);
            if ($chunk === false) {
                fclose($handle);
                throw new \RuntimeException(sprintf('Unable to read log file: %s', $path));
            }

            $buffer = $chunk . $buffer;
        }

        fclose($handle);

        $allLines = preg_split("/\r\n|\n|\r/", $buffer) ?: [];
        $allLines = array_map(static fn (string $line): string => rtrim($line, "\r\n"), $allLines);

        while ($allLines !== [] && end($allLines) === '') {
            array_pop($allLines);
        }

        return array_slice($allLines, -$lines);
    }

    private function formatBytes(int $bytes): string
    {
        if ($bytes <= 0) {
            return '0 B';
        }

        $units = ['B', 'KB', 'MB', 'GB', 'TB'];
        $power = min((int) floor(log($bytes, 1024)), count($units) - 1);
        $value = $bytes / (1024 ** $power);

        return sprintf('%.1f %s', $value, $units[$power]);
    }
}
