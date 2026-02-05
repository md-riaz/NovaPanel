<?php

namespace Tests\Unit\Infrastructure\Adapters;

use App\Infrastructure\Adapters\PureFtpdAdapter;
use App\Contracts\ShellInterface;
use App\Domain\Entities\FtpUser;
use PHPUnit\Framework\TestCase;

class PureFtpdAdapterTest extends TestCase
{
    private ShellInterface $shellMock;
    private PureFtpdAdapter $adapter;

    protected function setUp(): void
    {
        $this->shellMock = $this->createMock(ShellInterface::class);
        $this->adapter = new PureFtpdAdapter($this->shellMock);
    }

    public function testCreateUserChecksIfPureFtpdIsInstalled(): void
    {
        // Arrange
        $ftpUser = new FtpUser(
            userId: 1,
            username: 'testuser',
            homeDirectory: '/opt/novapanel/sites/test',
            enabled: true
        );

        // Mock pure-pw check command - simulate pure-ftpd not installed
        $this->shellMock->expects($this->once())
            ->method('execute')
            ->with('bash', ['-c', 'command -v pure-pw'])
            ->willReturn(['output' => '', 'exitCode' => 1]);

        // Assert
        $this->expectException(\RuntimeException::class);
        $this->expectExceptionMessage('Pure-FTPd is not installed');

        // Act
        $this->adapter->createUser($ftpUser, 'password123');
    }

    public function testCreateUserGetsUidAndGidOfPanelUser(): void
    {
        // Arrange
        $ftpUser = new FtpUser(
            userId: 1,
            username: 'testuser',
            homeDirectory: '/opt/novapanel/sites/test',
            enabled: true
        );

        // Mock commands
        $this->shellMock->method('execute')
            ->willReturnCallback(function ($cmd, $args) {
                if ($cmd === 'bash' && isset($args[0]) && $args[0] === '-c') {
                    return ['output' => '/usr/bin/pure-pw', 'exitCode' => 0];
                }
                if ($cmd === 'id' && $args[0] === '-u') {
                    return ['output' => '1000', 'exitCode' => 0];
                }
                if ($cmd === 'id' && $args[0] === '-g') {
                    return ['output' => '1000', 'exitCode' => 0];
                }
                return ['output' => '', 'exitCode' => 1];
            });

        $this->shellMock->expects($this->once())
            ->method('executeSudo')
            ->willReturn(['output' => '', 'exitCode' => 0]);

        // Act
        $result = $this->adapter->createUser($ftpUser, 'password123');

        // Assert
        $this->assertTrue($result);
    }

    public function testCreateUserThrowsExceptionWhenPanelUserNotFound(): void
    {
        // Arrange
        $ftpUser = new FtpUser(
            userId: 1,
            username: 'testuser',
            homeDirectory: '/opt/novapanel/sites/test',
            enabled: true
        );

        $this->shellMock->method('execute')
            ->willReturnCallback(function ($cmd, $args) {
                if ($cmd === 'bash' && isset($args[0]) && $args[0] === '-c') {
                    return ['output' => '/usr/bin/pure-pw', 'exitCode' => 0];
                }
                if ($cmd === 'id') {
                    return ['output' => 'id: novapanel: no such user', 'exitCode' => 1];
                }
                return ['output' => '', 'exitCode' => 1];
            });

        // Assert
        $this->expectException(\RuntimeException::class);
        $this->expectExceptionMessage('Failed to get UID for novapanel');

        // Act
        $this->adapter->createUser($ftpUser, 'password123');
    }

    public function testDeleteUserCallsPurePasswordCommand(): void
    {
        // Arrange
        $ftpUser = new FtpUser(
            userId: 1,
            username: 'testuser',
            homeDirectory: '/opt/novapanel/sites/test',
            enabled: true
        );

        $this->shellMock->expects($this->once())
            ->method('executeSudo')
            ->with('pure-pw', ['userdel', 'testuser', '-m'])
            ->willReturn(['output' => '', 'exitCode' => 0]);

        // Act
        $result = $this->adapter->deleteUser($ftpUser);

        // Assert
        $this->assertTrue($result);
    }

    public function testDeleteUserThrowsExceptionOnFailure(): void
    {
        // Arrange
        $ftpUser = new FtpUser(
            userId: 1,
            username: 'testuser',
            homeDirectory: '/opt/novapanel/sites/test',
            enabled: true
        );

        $this->shellMock->expects($this->once())
            ->method('executeSudo')
            ->willReturn(['output' => 'User not found', 'exitCode' => 1]);

        // Assert
        $this->expectException(\RuntimeException::class);
        $this->expectExceptionMessage('Failed to delete FTP user');

        // Act
        $this->adapter->deleteUser($ftpUser);
    }

    public function testUpdateUserCallsModCommand(): void
    {
        // Arrange
        $ftpUser = new FtpUser(
            userId: 1,
            username: 'testuser',
            homeDirectory: '/opt/novapanel/sites/test/new',
            enabled: true
        );

        $this->shellMock->expects($this->once())
            ->method('executeSudo')
            ->with('pure-pw', ['usermod', 'testuser', '-d', '/opt/novapanel/sites/test/new', '-m'])
            ->willReturn(['output' => '', 'exitCode' => 0]);

        // Act
        $result = $this->adapter->updateUser($ftpUser);

        // Assert
        $this->assertTrue($result);
    }

    public function testChangePasswordHandlesPasswordCorrectly(): void
    {
        // Arrange
        $ftpUser = new FtpUser(
            userId: 1,
            username: 'testuser',
            homeDirectory: '/opt/novapanel/sites/test',
            enabled: true
        );

        $this->shellMock->expects($this->once())
            ->method('executeSudo')
            ->willReturn(['output' => '', 'exitCode' => 0]);

        // Act
        $result = $this->adapter->changePassword($ftpUser, 'newpassword123');

        // Assert
        $this->assertTrue($result);
    }
}
