<?php

namespace App\Support;

class SecurityManagerService
{
    /**
     * @var array<string, array<string, string>>
     */
    private array $actions = [
        'ufw_reload' => [
            'label' => 'Reload UFW',
            'component' => 'ufw',
            'command' => 'sudo -n ufw reload',
        ],
        'fail2ban_restart' => [
            'label' => 'Restart fail2ban',
            'component' => 'fail2ban',
            'command' => 'sudo -n systemctl restart fail2ban',
        ],
    ];

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
        $result = $this->execCommand($definition['command']);

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

        $command = $this->canRunPrivilegedActions() ? 'sudo -n ufw status' : 'ufw status';
        $result = $this->execCommand($command);
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
            $state = trim((string) shell_exec('systemctl show fail2ban --property=ActiveState --value 2>/dev/null'));

            if ($state !== '') {
                return $this->statusPayload(
                    $state,
                    $state === 'active' ? 'success' : 'warning',
                    sprintf('fail2ban service state: %s', $state)
                );
            }
        }

        $result = $this->execCommand('fail2ban-client ping');

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

        $result = $this->execCommand('sudo -n true');
        return $result['exit_code'] === 0;
    }

    private function commandExists(string $command): bool
    {
        $result = $this->execCommand(sprintf('command -v %s', escapeshellarg($command)));
        return trim($result['output']) !== '';
    }

    private function systemdUnitExists(string $unit): bool
    {
        if (!$this->commandExists('systemctl')) {
            return false;
        }

        $state = trim((string) shell_exec(sprintf('systemctl show %s --property=LoadState --value 2>/dev/null', escapeshellarg($unit))));
        return $state !== '' && $state !== 'not-found';
    }

    /**
     * @return array<string, mixed>
     */
    private function execCommand(string $command): array
    {
        $output = [];
        $exitCode = 0;
        exec($command . ' 2>&1', $output, $exitCode);

        return [
            'output' => implode("\n", $output),
            'exit_code' => $exitCode,
        ];
    }
}
