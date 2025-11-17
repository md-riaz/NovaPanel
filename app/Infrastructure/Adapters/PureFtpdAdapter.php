<?php

namespace App\Infrastructure\Adapters;

use App\Contracts\FtpManagerInterface;
use App\Contracts\ShellInterface;
use App\Domain\Entities\FtpUser;

/**
 * Pure-FTPd Adapter - manages FTP users via pure-pw command
 */
class PureFtpdAdapter implements FtpManagerInterface
{
    private const PUREFTPD_PASSWD = '/etc/pure-ftpd/pureftpd.passwd';
    private const PUREFTPD_PUREDB = '/etc/pure-ftpd/pureftpd.pdb';

    public function __construct(
        private ShellInterface $shell
    ) {}

    public function createUser(FtpUser $user, string $password): bool
    {
        try {
            // Check if pure-ftpd is installed
            $checkResult = $this->shell->execute('bash', ['-c', 'command -v pure-pw']);
            if ($checkResult['exitCode'] !== 0) {
                throw new \RuntimeException("Pure-FTPd is not installed. Please install pure-ftpd package first.");
            }

            // pure-pw useradd username -u uid -g gid -d /home/directory -m
            // For NovaPanel, all FTP users should use the panel user's UID/GID
            $panelUser = 'novapanel';
            
            // Get UID and GID of panel user
            $result = $this->shell->execute('id', ['-u', $panelUser]);
            if ($result['exitCode'] !== 0) {
                throw new \RuntimeException("Failed to get UID for {$panelUser}. Panel user may not exist.");
            }
            $uid = trim($result['output']);
            
            $result = $this->shell->execute('id', ['-g', $panelUser]);
            if ($result['exitCode'] !== 0) {
                throw new \RuntimeException("Failed to get GID for {$panelUser}. Panel user may not exist.");
            }
            $gid = trim($result['output']);
            
            // Create FTP user with pure-pw
            // Note: pure-pw expects password via stdin or -m flag
            $tempPassFile = tempnam(sys_get_temp_dir(), 'ftppass_');
            file_put_contents($tempPassFile, $password . "\n" . $password . "\n");
            
            $cmd = sprintf(
                "pure-pw useradd %s -u %s -g %s -d %s -m < %s",
                escapeshellarg($user->username),
                escapeshellarg($uid),
                escapeshellarg($gid),
                escapeshellarg($user->homeDirectory),
                escapeshellarg($tempPassFile)
            );
            
            // Execute as root via sudo
            $result = $this->shell->executeSudo('bash', ['-c', $cmd]);
            
            // Clean up temp file
            @unlink($tempPassFile);
            
            if ($result['exitCode'] !== 0) {
                throw new \RuntimeException("Failed to create FTP user: " . $result['output']);
            }
            
            return true;
        } catch (\Exception $e) {
            error_log("Pure-FTPd user creation failed: " . $e->getMessage());
            return false;
        }
    }

    public function updateUser(FtpUser $user): bool
    {
        try {
            // pure-pw usermod allows changing home directory
            $result = $this->shell->executeSudo('pure-pw', [
                'usermod',
                $user->username,
                '-d',
                $user->homeDirectory,
                '-m'
            ]);
            
            if ($result['exitCode'] !== 0) {
                throw new \RuntimeException("Failed to update FTP user: " . $result['output']);
            }
            
            return true;
        } catch (\Exception $e) {
            error_log("Pure-FTPd user update failed: " . $e->getMessage());
            return false;
        }
    }

    public function deleteUser(FtpUser $user): bool
    {
        try {
            // pure-pw userdel username -m
            $result = $this->shell->executeSudo('pure-pw', [
                'userdel',
                $user->username,
                '-m'
            ]);
            
            if ($result['exitCode'] !== 0) {
                throw new \RuntimeException("Failed to delete FTP user: " . $result['output']);
            }
            
            return true;
        } catch (\Exception $e) {
            error_log("Pure-FTPd user deletion failed: " . $e->getMessage());
            return false;
        }
    }

    public function changePassword(FtpUser $user, string $password): bool
    {
        try {
            // pure-pw passwd username
            $tempPassFile = tempnam(sys_get_temp_dir(), 'ftppass_');
            file_put_contents($tempPassFile, $password . "\n" . $password . "\n");
            
            $cmd = sprintf(
                "pure-pw passwd %s -m < %s",
                escapeshellarg($user->username),
                escapeshellarg($tempPassFile)
            );
            
            $result = $this->shell->executeSudo('bash', ['-c', $cmd]);
            
            // Clean up temp file
            @unlink($tempPassFile);
            
            if ($result['exitCode'] !== 0) {
                throw new \RuntimeException("Failed to change FTP password: " . $result['output']);
            }
            
            return true;
        } catch (\Exception $e) {
            error_log("Pure-FTPd password change failed: " . $e->getMessage());
            return false;
        }
    }
}
