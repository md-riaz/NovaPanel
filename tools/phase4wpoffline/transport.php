<?php
// This file is reachable only from the fixed lab bootstrap, not production.
require_once WP_CLI_ROOT . '/php/class-wp-cli.php';
require WP_CLI_ROOT . '/bundle/rmccue/requests/src/Autoload.php';
\WpOrg\Requests\Autoload::register();
final class LabChecksums implements \WpOrg\Requests\Transport {
    public function request($url, $headers = [], $data = [], $options = []) {
        if ($url !== LAB_URL || ($options['type'] ?? '') !== 'GET' || !empty($data)) {
            throw new RuntimeException('offline request refused');
        }
        $body = file_get_contents('/lab/checksums.json');
        return "HTTP/1.1 200 OK\r\nContent-Type: application/json\r\nContent-Length: " . strlen($body) . "\r\n\r\n" . $body;
    }
    public function request_multiple($requests, $options) {
        throw new RuntimeException('offline multiple requests refused');
    }
    public static function test($capabilities = []) { return true; }
}
WP_CLI::add_hook('http_request_options', static function ($options) {
    $options['transport'] = new LabChecksums();
    return $options;
});
// Exercise the refusal behavior before any official command is admitted.
$fixture = new LabChecksums();
foreach ([LAB_URL . '&extra=1', 'https://example.invalid/', str_replace('7.1.4', '7.1.3', LAB_URL)] as $wrong) {
    try {
        $fixture->request($wrong, [], [], ['type' => 'GET']);
        exit(79);
    } catch (RuntimeException $expected) {}
}
try {
    $fixture->request(LAB_URL, [], [], ['type' => 'POST']);
    exit(79);
} catch (RuntimeException $expected) {}
try {
    $fixture->request_multiple([], []);
    exit(79);
} catch (RuntimeException $expected) {}
unset($fixture, $wrong);
