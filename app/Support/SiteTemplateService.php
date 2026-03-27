<?php

namespace App\Support;

class SiteTemplateService
{
    /**
     * @return array<int, array<string, string>>
     */
    public function templates(): array
    {
        return [
            [
                'key' => 'basic_php',
                'name' => 'Basic PHP app',
                'summary' => 'A clean starter with a PHP landing page and writable tmp directory.',
                'stack' => 'PHP-FPM + Nginx',
            ],
            [
                'key' => 'wordpress',
                'name' => 'WordPress-ready deployment',
                'summary' => 'Bootstraps a WordPress-ready document root with setup notes and content folders.',
                'stack' => 'PHP + MySQL',
            ],
            [
                'key' => 'static_site',
                'name' => 'Static site',
                'summary' => 'Minimal HTML/CSS starter for landing pages, docs, and brochure sites.',
                'stack' => 'Nginx only',
            ],
        ];
    }

    /**
     * @return array<string, string>
     */
    public function find(string $key): array
    {
        foreach ($this->templates() as $template) {
            if ($template['key'] === $key) {
                return $template;
            }
        }

        throw new \InvalidArgumentException('Unknown site template selected.');
    }

    /**
     * @param array<string, string> $context
     */
    public function apply(string $key, string $documentRoot, array $context): void
    {
        $this->find($key);

        foreach ($this->directoriesFor($key) as $directory) {
            $path = rtrim($documentRoot, '/') . '/' . ltrim($directory, '/');
            if (!is_dir($path) && !mkdir($path, 0775, true) && !is_dir($path)) {
                throw new \RuntimeException(sprintf('Failed to create template directory: %s', $path));
            }
        }

        foreach ($this->filesFor($key, $context) as $relativePath => $content) {
            $path = rtrim($documentRoot, '/') . '/' . ltrim($relativePath, '/');
            $parent = dirname($path);

            if (!is_dir($parent) && !mkdir($parent, 0775, true) && !is_dir($parent)) {
                throw new \RuntimeException(sprintf('Failed to create template directory: %s', $parent));
            }

            if (file_put_contents($path, $content, LOCK_EX) === false) {
                throw new \RuntimeException(sprintf('Failed to write template file: %s', $path));
            }
        }
    }

    /**
     * @return array<int, string>
     */
    private function directoriesFor(string $key): array
    {
        return match ($key) {
            'basic_php' => ['tmp', 'public'],
            'wordpress' => ['wp-content', 'wp-content/uploads', 'wp-content/plugins', 'wp-content/themes'],
            'static_site' => ['assets', 'assets/css'],
            default => [],
        };
    }

    /**
     * @param array<string, string> $context
     * @return array<string, string>
     */
    private function filesFor(string $key, array $context): array
    {
        $domain = $this->normalizeDomain($context['domain'] ?? 'example.com');
        $owner = $this->normalizeOwner($context['owner'] ?? 'novapanel');
        $domainHtml = htmlspecialchars($domain, ENT_QUOTES, 'UTF-8');
        $ownerHtml = htmlspecialchars($owner, ENT_QUOTES, 'UTF-8');
        $domainPhp = var_export($domain, true);

        return match ($key) {
            'basic_php' => [
                'index.php' => <<<PHP
<?php

\$appName = 'NovaPanel Starter';
\$domain = {$domainPhp};
?>
<!doctype html>
<html lang="en">
<head>
    <meta charset="utf-8">
    <meta name="viewport" content="width=device-width, initial-scale=1">
    <title><?= htmlspecialchars(\$appName) ?></title>
    <style>
        body { font-family: Arial, sans-serif; margin: 3rem; background: #0f172a; color: #e2e8f0; }
        .card { max-width: 720px; background: #111827; border-radius: 16px; padding: 2rem; }
        code { color: #93c5fd; }
    </style>
</head>
<body>
    <div class="card">
        <p>Provisioned by NovaPanel for {$ownerHtml}</p>
        <h1><?= htmlspecialchars(\$appName) ?></h1>
        <p>Your site for <strong><?= htmlspecialchars(\$domain, ENT_QUOTES, 'UTF-8') ?></strong> is live and ready for application code.</p>
        <p>Use <code>tmp/</code> for uploads and local runtime files that should stay writable.</p>
    </div>
</body>
</html>
PHP,
                'README.md' => "# Basic PHP template\n\nThis site was scaffolded for {$domainHtml}. Replace `index.php` with your app entrypoint and keep writable data inside `tmp/`.\n",
            ],
            'wordpress' => [
                'index.php' => <<<PHP
<?php
if (file_exists(__DIR__ . '/wp-blog-header.php')) {
    require __DIR__ . '/wp-blog-header.php';
    return;
}
?>
<!doctype html>
<html lang="en">
<head>
    <meta charset="utf-8">
    <meta name="viewport" content="width=device-width, initial-scale=1">
    <title>WordPress ready</title>
    <style>
        body { font-family: Arial, sans-serif; margin: 3rem; background: #f8fafc; color: #0f172a; }
        .shell { max-width: 760px; background: #fff; border: 1px solid #cbd5e1; border-radius: 16px; padding: 2rem; }
        code { background: #e2e8f0; padding: 0.1rem 0.35rem; border-radius: 6px; }
    </style>
</head>
<body>
    <div class="shell">
        <p>NovaPanel created a WordPress-ready deployment for <strong>{$domainHtml}</strong>.</p>
        <h1>Finish WordPress setup</h1>
        <ol>
            <li>Create a MySQL database and user in NovaPanel.</li>
            <li>Upload or unpack WordPress core files into this document root.</li>
            <li>Copy <code>wp-config-sample.php</code> to <code>wp-config.php</code> and add your database credentials.</li>
            <li>Visit <code>/wp-admin/install.php</code> to complete the installer.</li>
        </ol>
    </div>
</body>
</html>
PHP,
                'wp-config-sample.php' => <<<PHP
<?php

define('DB_NAME', 'replace_me');
define('DB_USER', 'replace_me');
define('DB_PASSWORD', 'replace_me');
define('DB_HOST', '127.0.0.1');

define('WP_HOME', 'https://{$domainHtml}');
define('WP_SITEURL', 'https://{$domainHtml}');

$table_prefix = 'wp_';
PHP,
                'README.md' => "# WordPress-ready template\n\nProvisioned for {$domainHtml} and owned by {$ownerHtml}. Upload the official WordPress package into this document root, create `wp-config.php`, and finish the installer from `/wp-admin/install.php`.\n",
            ],
            'static_site' => [
                'index.html' => <<<HTML
<!doctype html>
<html lang="en">
<head>
    <meta charset="utf-8">
    <meta name="viewport" content="width=device-width, initial-scale=1">
    <title>{$domainHtml}</title>
    <link rel="stylesheet" href="/assets/css/site.css">
</head>
<body>
    <main>
        <span class="eyebrow">NovaPanel static template</span>
        <h1>{$domainHtml}</h1>
        <p>Launch a documentation site, product landing page, or marketing microsite quickly.</p>
    </main>
</body>
</html>
HTML,
                'assets/css/site.css' => <<<CSS
body {
    margin: 0;
    font-family: Arial, sans-serif;
    min-height: 100vh;
    display: grid;
    place-items: center;
    background: linear-gradient(135deg, #0f172a, #1d4ed8);
    color: #fff;
}

main {
    text-align: center;
    max-width: 720px;
    padding: 2rem;
}

.eyebrow {
    display: inline-block;
    margin-bottom: 1rem;
    font-size: 0.875rem;
    letter-spacing: 0.08em;
    text-transform: uppercase;
    color: #bfdbfe;
}
CSS,
                'README.md' => "# Static site template\n\nThis site was scaffolded for {$domainHtml}. Replace `index.html` and `assets/css/site.css` with your static assets.\n",
            ],
            default => [],
        };
    }

    private function normalizeDomain(string $domain): string
    {
        $normalized = strtolower(trim($domain));
        if ($normalized === '') {
            return 'example.com';
        }

        $isValid = filter_var($normalized, FILTER_VALIDATE_DOMAIN, FILTER_FLAG_HOSTNAME) !== false;
        return $isValid ? $normalized : 'example.com';
    }

    private function normalizeOwner(string $owner): string
    {
        $normalized = preg_replace('/[^a-zA-Z0-9._-]/', '', trim($owner)) ?? '';
        return $normalized !== '' ? $normalized : 'novapanel';
    }
}
