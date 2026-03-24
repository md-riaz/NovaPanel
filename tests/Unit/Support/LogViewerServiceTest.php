<?php

namespace Tests\Unit\Support;

use App\Support\LogViewerService;
use PHPUnit\Framework\TestCase;

class LogViewerServiceTest extends TestCase
{
    private string $projectRoot;

    protected function setUp(): void
    {
        $this->projectRoot = sys_get_temp_dir() . '/novapanel-log-viewer-' . uniqid('', true);
        mkdir($this->projectRoot . '/storage/logs', 0777, true);
    }

    protected function tearDown(): void
    {
        $this->removeDirectory($this->projectRoot);
    }

    public function testReadReturnsTailOfPanelAuditLog(): void
    {
        $lines = [];
        for ($i = 1; $i <= 10; $i++) {
            $lines[] = 'audit line ' . $i;
        }

        file_put_contents($this->projectRoot . '/storage/logs/audit.log', implode("\n", $lines));

        $service = new LogViewerService($this->projectRoot);
        $result = $service->read('panel_audit', 3);

        $this->assertTrue($result['available']);
        $this->assertStringContainsString('audit line 8', $result['content']);
        $this->assertStringContainsString('audit line 10', $result['content']);
        $this->assertStringNotContainsString('audit line 1', $result['content']);
    }

    public function testReadReturnsHelpfulMessageWhenPathMissing(): void
    {
        $service = new LogViewerService($this->projectRoot);
        $result = $service->read('panel_app', 100);

        $this->assertFalse($result['available']);
        $this->assertStringContainsString('not available', strtolower($result['content']));
    }

    private function removeDirectory(string $path): void
    {
        if (!is_dir($path)) {
            return;
        }

        $items = scandir($path) ?: [];
        foreach ($items as $item) {
            if ($item === '.' || $item === '..') {
                continue;
            }

            $fullPath = $path . '/' . $item;
            if (is_dir($fullPath)) {
                $this->removeDirectory($fullPath);
            } else {
                unlink($fullPath);
            }
        }

        rmdir($path);
    }
}
