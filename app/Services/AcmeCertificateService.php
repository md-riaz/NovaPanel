<?php

namespace App\Services;

use App\Contracts\ShellInterface;
use App\Contracts\WebServerManagerInterface;
use App\Domain\Entities\Site;
use App\Repositories\SiteRepository;
use App\Support\AuditLogger;
use App\Support\Env;

class AcmeCertificateService
{
    private const LOG_FILE = __DIR__ . '/../../storage/logs/certificates.log';

    public function __construct(
        private SiteRepository $siteRepository,
        private WebServerManagerInterface $webServerManager,
        private ShellInterface $shell
    ) {}

    public function issue(
        Site $site,
        string $provider = 'letsencrypt',
        string $validationMethod = 'webroot',
        bool $autoRenew = true,
        bool $forceHttps = false,
        bool $forceRenewal = false
    ): Site {
        $this->assertSupportedProvider($provider);
        $this->assertSupportedValidationMethod($validationMethod);

        $site->certificateProvider = $provider;
        $site->certificateValidationMethod = $validationMethod;
        $site->certificateAutoRenew = $autoRenew;
        $site->forceHttps = $forceHttps;
        $site->certificateStatus = $forceRenewal ? 'renewing' : 'pending';
        $site->lastCertificateError = null;
        $this->siteRepository->update($site);

        try {
            if ($validationMethod === 'webroot') {
                $this->prepareWebroot($site);
            }

            $command = $this->buildIssueCommand($site, $validationMethod, $forceRenewal);
            $result = $this->shell->executeSudo('bash', ['-lc', $command]);

            if ($result['exitCode'] !== 0) {
                throw new \RuntimeException(trim($result['output']) ?: 'ACME client returned a non-zero exit code.');
            }
        } catch (\Throwable $exception) {
            $sanitizedError = $this->sanitizeCertbotOutput($exception->getMessage());

            $site->certificateStatus = 'failed';
            $site->lastCertificateError = $sanitizedError;
            $this->siteRepository->update($site);

            $this->log(sprintf('Certificate issue/renew failed for %s: %s', $site->domain, $sanitizedError));
            AuditLogger::log('certificate.failed', "Certificate request failed for {$site->domain}", [
                'site_id' => $site->id,
                'provider' => $provider,
                'validation_method' => $validationMethod,
                'force_renewal' => $forceRenewal,
                'output' => $sanitizedError,
            ]);

            throw $exception instanceof \RuntimeException
                ? $exception
                : new \RuntimeException($sanitizedError, 0, $exception);
        }

        $this->refreshCertificateState($site, 'active');
        AuditLogger::log($forceRenewal ? 'certificate.renewed' : 'certificate.issued', "Certificate is active for {$site->domain}", [
            'site_id' => $site->id,
            'provider' => $provider,
            'validation_method' => $validationMethod,
            'expires_at' => $site->certificateExpiresAt,
        ]);

        return $site;
    }

    public function renew(Site $site, bool $forceRenewal = true): Site
    {
        return $this->issue(
            $site,
            $site->certificateProvider ?? 'letsencrypt',
            $site->certificateValidationMethod ?? 'webroot',
            $site->certificateAutoRenew ?? true,
            $site->forceHttps ?? false,
            $forceRenewal
        );
    }

    public function revoke(Site $site): Site
    {
        if (empty($site->certificatePath)) {
            throw new \RuntimeException('No installed certificate is recorded for this site.');
        }

        $result = $this->shell->executeSudo('bash', ['-lc', sprintf(
            'certbot revoke --non-interactive --cert-path %s --delete-after-revoke',
            escapeshellarg($site->certificatePath)
        )]);

        if ($result['exitCode'] !== 0) {
            $message = trim($result['output']) ?: 'Unable to revoke certificate.';
            $site->lastCertificateError = $this->sanitizeCertbotOutput($message);
            $site->certificateStatus = 'failed';
            $this->siteRepository->update($site);
            $this->log(sprintf('Certificate revoke failed for %s: %s', $site->domain, $site->lastCertificateError));
            throw new \RuntimeException($message);
        }

        $site->sslEnabled = false;
        $site->certificateStatus = 'revoked';
        $site->certificateExpiresAt = null;
        $site->certificatePath = null;
        $site->certificateKeyPath = null;
        $site->forceHttps = false;
        $site->lastCertificateRenewalAt = date('Y-m-d H:i:s');
        $site->lastCertificateError = null;

        $this->webServerManager->updateSite($site);
        $this->siteRepository->update($site);

        AuditLogger::log('certificate.revoked', "Certificate revoked for {$site->domain}", [
            'site_id' => $site->id,
        ]);

        return $site;
    }

    public function reinstall(Site $site): Site
    {
        $certificatePath = $this->defaultCertificatePath($site);
        $certificateKeyPath = $this->defaultCertificateKeyPath($site);

        $certExists = $this->shell->executeSudo('bash', ['-lc', 'test -f ' . escapeshellarg($certificatePath)])['exitCode'] === 0;
        $keyExists = $this->shell->executeSudo('bash', ['-lc', 'test -f ' . escapeshellarg($certificateKeyPath)])['exitCode'] === 0;
        if (!$certExists || !$keyExists) {
            throw new \RuntimeException('Certificate files were not found in the default Certbot live directory.');
        }

        $site->sslEnabled = true;
        $site->certificateProvider ??= 'letsencrypt';
        $site->certificateStatus = 'active';
        $site->certificatePath = $certificatePath;
        $site->certificateKeyPath = $certificateKeyPath;
        $site->lastCertificateRenewalAt = date('Y-m-d H:i:s');
        $site->lastCertificateError = null;
        $site->certificateExpiresAt = $this->readCertificateExpiry($site);

        $this->webServerManager->updateSite($site);
        $this->siteRepository->update($site);

        AuditLogger::log('certificate.reinstalled', "Certificate paths reinstalled for {$site->domain}", [
            'site_id' => $site->id,
            'cert_path' => $certificatePath,
        ]);

        return $site;
    }

    public function renewDueCertificates(int $withinDays = 30): array
    {
        $sites = $this->siteRepository->findCertificatesDueForRenewal($withinDays);
        $summary = ['processed' => 0, 'renewed' => 0, 'failed' => 0];

        foreach ($sites as $site) {
            $summary['processed']++;

            try {
                $this->renew($site, true);
                $summary['renewed']++;
            } catch (\Throwable $exception) {
                $summary['failed']++;
                $this->log(sprintf('Scheduled renewal failed for %s: %s', $site->domain, $this->sanitizeCertbotOutput($exception->getMessage())));
            }
        }

        return $summary;
    }

    private function refreshCertificateState(Site $site, string $status): void
    {
        $site->sslEnabled = true;
        $site->certificateStatus = $status;
        $site->certificatePath = $this->defaultCertificatePath($site);
        $site->certificateKeyPath = $this->defaultCertificateKeyPath($site);
        $site->certificateExpiresAt = $this->readCertificateExpiry($site);
        $site->lastCertificateRenewalAt = date('Y-m-d H:i:s');
        $site->lastCertificateError = null;

        $this->webServerManager->updateSite($site);
        $this->siteRepository->update($site);
    }

    private function prepareWebroot(Site $site): void
    {
        if (empty($site->documentRoot)) {
            throw new \RuntimeException('Webroot validation requires a document root.');
        }

        $challengePath = rtrim($site->documentRoot, '/') . '/.well-known/acme-challenge';
        $result = $this->shell->executeSudo('mkdir', ['-p', $challengePath]);

        if ($result['exitCode'] !== 0) {
            throw new \RuntimeException('Unable to prepare ACME challenge directory: ' . $result['output']);
        }
    }

    private function buildIssueCommand(Site $site, string $validationMethod, bool $forceRenewal): string
    {
        $base = [
            'certbot certonly',
            '--non-interactive',
            '--agree-tos',
            '--cert-name ' . escapeshellarg($site->domain),
            '-d ' . escapeshellarg($site->domain),
            '--keep-until-expiring',
        ];

        $acmeEmail = trim((string) Env::get('ACME_EMAIL', ''));
        if ($acmeEmail !== '') {
            $base[] = '--email ' . escapeshellarg($acmeEmail);
        } else {
            $base[] = '--register-unsafely-without-email';
        }

        if ($forceRenewal) {
            $base[] = '--force-renewal';
        }

        if ($validationMethod === 'dns') {
            $authHook = Env::get('ACME_DNS_AUTH_HOOK');
            $cleanupHook = Env::get('ACME_DNS_CLEANUP_HOOK');

            if (!$authHook || !$cleanupHook) {
                throw new \RuntimeException('DNS validation requires ACME_DNS_AUTH_HOOK and ACME_DNS_CLEANUP_HOOK in .env.php.');
            }

            $base[] = '--manual';
            $base[] = '--preferred-challenges dns';
            $base[] = '--manual-public-ip-logging-ok';
            $base[] = '--manual-auth-hook ' . escapeshellarg($authHook);
            $base[] = '--manual-cleanup-hook ' . escapeshellarg($cleanupHook);
        } else {
            $base[] = '--preferred-challenges http';
            $base[] = '--webroot';
            $base[] = '-w ' . escapeshellarg((string) $site->documentRoot);
        }

        return implode(' ', $base);
    }

    private function readCertificateExpiry(Site $site): ?string
    {
        $result = $this->shell->executeSudo('bash', ['-lc', sprintf(
            'openssl x509 -enddate -noout -in %s | cut -d= -f2',
            escapeshellarg($this->defaultCertificatePath($site))
        )]);

        if ($result['exitCode'] !== 0 || trim($result['output']) === '') {
            return null;
        }

        $timestamp = strtotime(trim($result['output']));

        return $timestamp ? date('Y-m-d H:i:s', $timestamp) : null;
    }

    private function defaultCertificatePath(Site $site): string
    {
        return sprintf('/etc/letsencrypt/live/%s/fullchain.pem', $site->domain);
    }

    private function defaultCertificateKeyPath(Site $site): string
    {
        return sprintf('/etc/letsencrypt/live/%s/privkey.pem', $site->domain);
    }

    private function assertSupportedProvider(string $provider): void
    {
        if ($provider !== 'letsencrypt') {
            throw new \InvalidArgumentException("Unsupported certificate provider: {$provider}");
        }
    }

    private function assertSupportedValidationMethod(string $validationMethod): void
    {
        if (!in_array($validationMethod, ['webroot', 'dns'], true)) {
            throw new \InvalidArgumentException("Unsupported certificate validation method: {$validationMethod}");
        }
    }

    private function sanitizeCertbotOutput(string $output): string
    {
        $sanitized = $this->safePregReplace('/\b(?:\d{1,3}\.){3}\d{1,3}\b/', '[redacted-ip]', $output);
        $sanitized = $this->safePregReplace('#/[A-Za-z0-9_.\-/]+#', '[redacted-path]', $sanitized);
        $sanitized = $this->safePregReplace('/([A-Za-z0-9_\-]{16,})/', '[redacted-token]', $sanitized);
        $sanitized = $this->safePregReplace('/\s+/', ' ', trim($sanitized));

        if (strlen($sanitized) > 500) {
            $sanitized = substr($sanitized, 0, 497) . '...';
        }

        return $sanitized;
    }

    private function safePregReplace(string $pattern, string $replacement, string $subject): string
    {
        $result = preg_replace($pattern, $replacement, $subject);
        if (!is_string($result)) {
            error_log(sprintf('preg_replace failed during certificate output sanitization (code: %d)', preg_last_error()));
        }

        return is_string($result) ? $result : $subject;
    }

    private function log(string $message): void
    {
        $line = sprintf('[%s] %s', date('Y-m-d H:i:s'), $message) . PHP_EOL;
        $command = sprintf(
            'mkdir -p %s && printf %s >> %s',
            escapeshellarg(dirname(self::LOG_FILE)),
            escapeshellarg($line),
            escapeshellarg(self::LOG_FILE)
        );

        $result = $this->shell->executeSudo('bash', ['-lc', $command]);
        if ($result['exitCode'] !== 0) {
            error_log('Failed to write certificate log: ' . $result['output']);
        }
    }
}
