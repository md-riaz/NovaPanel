<?php

namespace Tests\Unit\Infrastructure\Adapters;

use App\Contracts\ShellInterface;
use App\Domain\Entities\Site;
use App\Infrastructure\Adapters\NginxAdapter;
use PHPUnit\Framework\TestCase;
use ReflectionMethod;

class NginxAdapterTest extends TestCase
{
    private ShellInterface $shellMock;
    private NginxAdapter $adapter;
    private ReflectionMethod $generateMethod;

    protected function setUp(): void
    {
        $this->shellMock = $this->createMock(ShellInterface::class);
        $this->adapter = new NginxAdapter($this->shellMock);
        $this->generateMethod = new ReflectionMethod(NginxAdapter::class, 'generateVhostConfig');
        $this->generateMethod->setAccessible(true);
    }

    public function testGenerateVhostConfigCreatesHttpAndHttpsBlocksWhenCertificateIsActive(): void
    {
        $site = new Site(
            domain: 'example.com',
            documentRoot: '/opt/novapanel/sites/john/example.com',
            phpVersion: '8.2',
            sslEnabled: true,
            certificateStatus: 'active',
            certificatePath: '/etc/letsencrypt/live/example.com/fullchain.pem',
            certificateKeyPath: '/etc/letsencrypt/live/example.com/privkey.pem',
            forceHttps: true
        );

        $config = $this->generateMethod->invoke($this->adapter, $site);

        $this->assertSame(1, substr_count($config, 'listen 80;'));
        $this->assertSame(1, substr_count($config, 'listen 443 ssl;'));
        $this->assertStringContainsString('http2 on;', $config);
        $this->assertStringContainsString('if ($request_uri !~ "^/\\.well-known/acme-challenge/") {', $config);
        $this->assertStringContainsString('return 301 https://$host$request_uri;', $config);
        $this->assertStringContainsString('add_header Strict-Transport-Security', $config);
        $this->assertStringContainsString('ssl_certificate /etc/letsencrypt/live/example.com/fullchain.pem;', $config);
        $this->assertStringContainsString('location ^~ /.well-known/acme-challenge/', $config);
    }

    public function testGenerateVhostConfigKeepsHttpOnlyWhenNoActiveCertificateExists(): void
    {
        $site = new Site(
            domain: 'example.com',
            documentRoot: '/opt/novapanel/sites/john/example.com',
            phpVersion: '8.2',
            sslEnabled: false,
            certificateStatus: 'unissued',
            forceHttps: false
        );

        $config = $this->generateMethod->invoke($this->adapter, $site);

        $this->assertSame(1, substr_count($config, 'listen 80;'));
        $this->assertStringNotContainsString('listen 443 ssl;', $config);
        $this->assertStringContainsString('try_files $uri $uri/ /index.php?$query_string;', $config);
        $this->assertStringNotContainsString('return 301 https://$host$request_uri;', $config);
    }

    public function testGenerateVhostConfigIncludesHttpsBlockButNoRedirectWhenForceHttpsIsFalse(): void
    {
        $site = new Site(
            domain: 'example.com',
            documentRoot: '/opt/novapanel/sites/john/example.com',
            phpVersion: '8.2',
            sslEnabled: true,
            certificateStatus: 'active',
            certificatePath: '/etc/letsencrypt/live/example.com/fullchain.pem',
            certificateKeyPath: '/etc/letsencrypt/live/example.com/privkey.pem',
            forceHttps: false
        );

        $config = $this->generateMethod->invoke($this->adapter, $site);

        $this->assertSame(1, substr_count($config, 'listen 443 ssl;'));
        $this->assertStringContainsString('http2 on;', $config);
        $this->assertStringContainsString('try_files $uri $uri/ /index.php?$query_string;', $config);
        $this->assertStringNotContainsString('return 301 https://$host$request_uri;', $config);
    }

    public function testGenerateVhostConfigKeepsHttpOnlyWhenCertificatePending(): void
    {
        $site = new Site(
            domain: 'example.com',
            documentRoot: '/opt/novapanel/sites/john/example.com',
            phpVersion: '8.2',
            sslEnabled: true,
            certificateStatus: 'pending',
            forceHttps: false
        );

        $config = $this->generateMethod->invoke($this->adapter, $site);

        $this->assertSame(1, substr_count($config, 'listen 80;'));
        $this->assertStringNotContainsString('listen 443 ssl;', $config);
        $this->assertStringNotContainsString('return 301 https://$host$request_uri;', $config);
    }
}
