<?php

namespace Tests\Unit\Services;

use App\Contracts\ShellInterface;
use App\Contracts\WebServerManagerInterface;
use App\Domain\Entities\Site;
use App\Repositories\SiteRepository;
use App\Services\AcmeCertificateService;
use PHPUnit\Framework\TestCase;

class AcmeCertificateServiceTest extends TestCase
{
    private SiteRepository $siteRepositoryMock;
    private WebServerManagerInterface $webServerManagerMock;
    private ShellInterface $shellMock;
    private AcmeCertificateService $service;

    protected function setUp(): void
    {
        $this->siteRepositoryMock = $this->createMock(SiteRepository::class);
        $this->webServerManagerMock = $this->createMock(WebServerManagerInterface::class);
        $this->shellMock = $this->createMock(ShellInterface::class);
        $this->service = new AcmeCertificateService(
            $this->siteRepositoryMock,
            $this->webServerManagerMock,
            $this->shellMock
        );
    }

    public function testIssueBuildsWebrootCertbotWorkflowAndUpdatesSiteState(): void
    {
        $site = new Site(
            id: 1,
            domain: 'example.com',
            documentRoot: '/var/www/example.com',
            phpVersion: '8.2'
        );

        $this->siteRepositoryMock->expects($this->exactly(2))->method('update');
        $this->webServerManagerMock->expects($this->once())->method('updateSite')->with($site)->willReturn(true);

        $this->shellMock->expects($this->exactly(2))
            ->method('executeSudo')
            ->willReturnCallback(function (string $command, array $args): array {
                if ($command === 'mkdir') {
                    $this->assertSame(['-p', '/var/www/example.com/.well-known/acme-challenge'], $args);
                    return ['output' => '', 'exitCode' => 0];
                }

                if ($command === 'bash') {
                    $this->assertSame('-lc', $args[0]);
                    $this->assertStringContainsString('certbot certonly', $args[1]);
                    $this->assertStringContainsString('--webroot', $args[1]);
                    $this->assertStringContainsString('-d \'example.com\'', $args[1]);
                    return ['output' => '', 'exitCode' => 0];
                }

                $this->fail('Unexpected sudo command: ' . $command);
            });

        $this->shellMock->expects($this->once())
            ->method('execute')
            ->with('bash', $this->callback(function (array $args): bool {
                return $args[0] === '-lc' && str_contains($args[1], 'openssl x509 -enddate');
            }))
            ->willReturn(['output' => 'notAfter=Jun 30 12:00:00 2026 GMT', 'exitCode' => 0]);

        $result = $this->service->issue($site, 'letsencrypt', 'webroot', true, true);

        $this->assertTrue($result->sslEnabled);
        $this->assertSame('active', $result->certificateStatus);
        $this->assertSame('/etc/letsencrypt/live/example.com/fullchain.pem', $result->certificatePath);
        $this->assertSame('/etc/letsencrypt/live/example.com/privkey.pem', $result->certificateKeyPath);
        $this->assertSame('2026-06-30 12:00:00', $result->certificateExpiresAt);
        $this->assertNull($result->lastCertificateError);
        $this->assertTrue($result->forceHttps);
    }

    public function testIssueFailsForDnsValidationWhenHooksAreMissing(): void
    {
        $site = new Site(
            id: 1,
            domain: 'example.com',
            documentRoot: '/var/www/example.com',
            phpVersion: '8.2'
        );

        $this->siteRepositoryMock->expects($this->exactly(2))->method('update');
        $this->shellMock->expects($this->once())
            ->method('executeSudo')
            ->with('mkdir', ['-p', '/var/www/example.com/.well-known/acme-challenge'])
            ->willReturn(['output' => '', 'exitCode' => 0]);

        $this->expectException(\RuntimeException::class);
        $this->expectExceptionMessage('DNS validation requires ACME_DNS_AUTH_HOOK and ACME_DNS_CLEANUP_HOOK in .env.php.');

        $this->service->issue($site, 'letsencrypt', 'dns', true, false);
    }

    public function testSanitizeCertbotOutputRedactsSensitiveValues(): void
    {
        $raw = 'Failed to connect 10.0.0.1 using token ABCDEFGHIJKLMNOP and path /etc/letsencrypt/live/example.com/fullchain.pem';

        $method = new \ReflectionMethod($this->service, 'sanitizeCertbotOutput');
        $method->setAccessible(true);
        $sanitized = $method->invoke($this->service, $raw);

        $this->assertStringNotContainsString('10.0.0.1', $sanitized);
        $this->assertStringNotContainsString('ABCDEFGHIJKLMNOP', $sanitized);
        $this->assertStringNotContainsString('/etc/letsencrypt/live/example.com/fullchain.pem', $sanitized);
        $this->assertStringContainsString('[redacted-ip]', $sanitized);
        $this->assertStringContainsString('[redacted-token]', $sanitized);
        $this->assertStringContainsString('[redacted-path]', $sanitized);
    }
}
