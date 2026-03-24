<?php

namespace Tests\Unit\Support;

use App\Support\SiteTemplateService;
use PHPUnit\Framework\TestCase;

class SiteTemplateServiceTest extends TestCase
{
    private string $documentRoot;

    protected function setUp(): void
    {
        $this->documentRoot = sys_get_temp_dir() . '/novapanel-template-' . uniqid('', true);
        mkdir($this->documentRoot, 0777, true);
    }

    protected function tearDown(): void
    {
        $this->removeDirectory($this->documentRoot);
    }

    public function testApplyCreatesBasicPhpTemplateFiles(): void
    {
        $service = new SiteTemplateService();
        $service->apply('basic_php', $this->documentRoot, [
            'domain' => 'example.com',
            'owner' => 'alice',
        ]);

        $this->assertFileExists($this->documentRoot . '/index.php');
        $this->assertFileExists($this->documentRoot . '/README.md');
        $this->assertDirectoryExists($this->documentRoot . '/tmp');
        $this->assertStringContainsString('example.com', file_get_contents($this->documentRoot . '/index.php'));
    }

    public function testApplyCreatesStaticSiteAssets(): void
    {
        $service = new SiteTemplateService();
        $service->apply('static_site', $this->documentRoot, [
            'domain' => 'static.example.com',
            'owner' => 'alice',
        ]);

        $this->assertFileExists($this->documentRoot . '/index.html');
        $this->assertFileExists($this->documentRoot . '/assets/css/site.css');
        $this->assertStringContainsString('static.example.com', file_get_contents($this->documentRoot . '/index.html'));
    }

    public function testFindThrowsForUnknownTemplate(): void
    {
        $service = new SiteTemplateService();

        $this->expectException(\InvalidArgumentException::class);
        $service->find('unknown-template');
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
