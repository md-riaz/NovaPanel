<?php

namespace App\Support;

use App\Facades\App;
use App\Infrastructure\Shell\Shell;

class SecurityManagerService
{
    private Shell $shell;

    /**
     * @var array<string, array<string, string>>
     */
    private array $actions = [
        'ufw_reload' => [
            'label' => 'Reload UFW',
            'component' => 'ufw',
            'command' => 'ufw',
            'args' => 'reload',
            'sudo' => 'true',
        ],
        'fail2ban_restart' => [
            'label' => 'Restart fail2ban',
            'component' => 'fail2ban',
            'command' => 'systemctl',
            'args' => 'restart fail2ban',
            'sudo' => 'true',
        ],
    ];

    public function __construct(?Shell $shell = null)
    {
        $this->shell = $shell ?? App::shell();
    }

    /**
     * @return array<string, mixed>
     */
    public function overview(): array
    {
        $canManage = $this->canRunPrivilegedActions();
        $ufwInstalled = $this->commandExists('ufw');
        $fail2banInstalled = $this->commandExists('fail2ban-client') || $this->systemdUnitExists('fail2ban');

        return [
            'can_manage' => $canManage,
            'components' => [
                [
                    'key' => 'ufw',
                    'label' => 'UFW firewall',
                    'installed' => $ufwInstalled,
                    'status' => $this->ufwStatus($ufwInstalled),
                    'actions' => $ufwInstalled ? [$this->withKey('ufw_reload')] : [],
                ],
                [
                    'key' => 'fail2ban',
                    'label' => 'fail2ban',
                    'installed' => $fail2banInstalled,
                    'status' => $this->fail2banStatus($fail2banInstalled),
                    'actions' => $fail2banInstalled ? [$this->withKey('fail2ban_restart')] : [],
                ],
            ],
        ];
    }

    /**
     * @return array<string, mixed>
     */
    public function runAction(string $action): array
    {
        if (!isset($this->actions[$action])) {
            throw new \InvalidArgumentException('Unsupported security action requested.');
        }

        if (!$this->canRunPrivilegedActions()) {
            throw new \RuntimeException('Controlled actions require passwordless sudo for the panel user.');
        }

        $definition = $this->actions[$action];
        $result = $this->execCommand(
            $definition['command'],
            $definition['args'] !== '' ? explode(' ', $definition['args']) : [],
            ($definition['sudo'] ?? 'false') === 'true'
        );

        if ($result['exit_code'] !== 0) {
            throw new \RuntimeException(trim($result['output']) ?: 'Security action failed.');
        }

        return [
            'label' => $definition['label'],
            'output' => trim($result['output']) ?: $definition['label'] . ' completed successfully.',
        ];
    }


    /**
     * @return array<string, string>
     */
    private function withKey(string $action): array
    {
        return ['key' => $action] + $this->actions[$action];
    }

    /**
     * @return array<string, mixed>
     */
    private function ufwStatus(bool $installed): array
    {
        if (!$installed) {
            return $this->statusPayload('not installed', 'secondary', 'UFW is not installed on this host.');
        }

        $result = $this->execCommand('ufw', ['status'], $this->canRunPrivilegedActions());
        $output = trim($result['output']);

        if (stripos($output, 'Status: active') !== false) {
            return $this->statusPayload('active', 'success', $output);
        }

        if (stripos($output, 'Status: inactive') !== false) {
            return $this->statusPayload('inactive', 'warning', $output);
        }

        if ($output === '') {
            return $this->statusPayload('unknown', 'secondary', 'Unable to determine UFW status.');
        }

        return $this->statusPayload('unknown', 'secondary', $output);
    }

    /**
     * @return array<string, mixed>
     */
    private function fail2banStatus(bool $installed): array
    {
        if (!$installed) {
            return $this->statusPayload('not installed', 'secondary', 'fail2ban is not installed on this host.');
        }

        if ($this->commandExists('systemctl')) {
            $result = $this->execCommand('systemctl', ['show', 'fail2ban', '--property=ActiveState', '--value']);
            $state = ($result['exit_code'] === 0) ? trim($result['output']) : '';

            if ($state !== '') {
                return $this->statusPayload(
                    $state,
                    $state === 'active' ? 'success' : 'warning',
                    sprintf('fail2ban service state: %s', $state)
                );
            }
        }

        $result = $this->execCommand('fail2ban-client', ['ping']);

        if ($result['exit_code'] === 0 && stripos($result['output'], 'Server replied') !== false) {
            return $this->statusPayload('active', 'success', trim($result['output']));
        }

        return $this->statusPayload('inactive', 'warning', trim($result['output']) ?: 'fail2ban did not respond to ping.');
    }

    /**
     * @return array<string, mixed>
     */
    private function statusPayload(string $state, string $badge, string $details): array
    {
        return [
            'state' => $state,
            'badge' => $badge,
            'details' => $details,
        ];
    }

    private function canRunPrivilegedActions(): bool
    {
        if (function_exists('posix_geteuid') && posix_geteuid() === 0) {
            return true;
        }

        $result = $this->execCommand('systemctl', ['--version'], true);
        return $result['exit_code'] === 0;
    }

    private function commandExists(string $command): bool
    {
        $result = $this->execCommand('bash', ['-lc', sprintf('command -v %s', escapeshellarg($command))]);
        return trim($result['output']) !== '';
    }

    private function systemdUnitExists(string $unit): bool
    {
        if (!$this->commandExists('systemctl')) {
            return false;
        }

        $result = $this->execCommand('systemctl', ['show', $unit, '--property=LoadState', '--value']);
        $state = ($result['exit_code'] === 0) ? trim($result['output']) : '';
        return $state !== '' && $state !== 'not-found';
    }

    /**
     * @return array<string, mixed>
     */
    private function execCommand(string $command, array $args = [], bool $sudo = false): array
    {
        if ($sudo) {
            $result = $this->shell->executeSudo($command, $args);
        } else {
            $result = $this->shell->execute($command, $args);
        }

        return [
            'output' => (string) ($result['output'] ?? ''),
            'exit_code' => (int) ($result['exitCode'] ?? 1),
        ];
    }
}
