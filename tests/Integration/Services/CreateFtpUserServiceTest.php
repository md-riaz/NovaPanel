<?php

namespace Tests\Integration\Services;

use App\Services\CreateFtpUserService;
use App\Repositories\FtpUserRepository;
use App\Repositories\UserRepository;
use App\Contracts\FtpManagerInterface;
use App\Domain\Entities\FtpUser;
use App\Domain\Entities\User;
use PHPUnit\Framework\TestCase;

class CreateFtpUserServiceTest extends TestCase
{
    private FtpUserRepository $ftpUserRepoMock;
    private UserRepository $userRepoMock;
    private FtpManagerInterface $ftpManagerMock;
    private CreateFtpUserService $service;

    protected function setUp(): void
    {
        $this->ftpUserRepoMock = $this->createMock(FtpUserRepository::class);
        $this->userRepoMock = $this->createMock(UserRepository::class);
        $this->ftpManagerMock = $this->createMock(FtpManagerInterface::class);

        $this->service = new CreateFtpUserService(
            $this->ftpUserRepoMock,
            $this->userRepoMock,
            $this->ftpManagerMock
        );
    }

    public function testExecuteValidatesFtpUsername(): void
    {
        $this->expectException(\InvalidArgumentException::class);
        $this->expectExceptionMessage('Invalid FTP username format');

        // Invalid username with special characters
        $this->service->execute(1, 'test@user!', 'password', '/opt/novapanel/sites/test');
    }

    public function testExecuteValidatesHomeDirectory(): void
    {
        // Mock user exists
        $user = $this->createMock(User::class);
        $this->userRepoMock->method('find')->willReturn($user);

        // Mock FTP username is available
        $this->ftpUserRepoMock->method('findByUsername')->willReturn(null);

        $this->expectException(\InvalidArgumentException::class);
        $this->expectExceptionMessage('Home directory must be within /opt/novapanel/sites/');

        // Invalid home directory outside allowed path
        $this->service->execute(1, 'testuser', 'password', '/home/testuser');
    }

    public function testExecuteChecksIfFtpUserAlreadyExists(): void
    {
        // Mock existing FTP user
        $existingFtpUser = new FtpUser(
            userId: 1,
            username: 'testuser',
            homeDirectory: '/opt/novapanel/sites/test',
            enabled: true
        );
        $this->ftpUserRepoMock->method('findByUsername')->willReturn($existingFtpUser);

        $this->expectException(\RuntimeException::class);
        $this->expectExceptionMessage("FTP user with username 'testuser' already exists");

        $this->service->execute(1, 'testuser', 'password', '/opt/novapanel/sites/test');
    }

    public function testExecuteChecksIfUserExists(): void
    {
        // Mock FTP username is available
        $this->ftpUserRepoMock->method('findByUsername')->willReturn(null);

        // Mock user does not exist
        $this->userRepoMock->method('find')->willReturn(null);

        $this->expectException(\RuntimeException::class);
        $this->expectExceptionMessage('User not found');

        $this->service->execute(1, 'testuser', 'password', '/opt/novapanel/sites/test');
    }

    public function testExecuteCreatesAndReturnsFtpUser(): void
    {
        // Mock user exists
        $user = $this->createMock(User::class);
        $this->userRepoMock->method('find')->willReturn($user);

        // Mock FTP username is available
        $this->ftpUserRepoMock->method('findByUsername')->willReturn(null);

        // Mock FTP user creation
        $ftpUser = new FtpUser(
            userId: 1,
            username: 'testuser',
            homeDirectory: '/opt/novapanel/sites/test',
            enabled: true,
            id: 1
        );
        $this->ftpUserRepoMock->method('create')->willReturn($ftpUser);

        // Mock FTP manager succeeds
        $this->ftpManagerMock->expects($this->once())
            ->method('createUser')
            ->with($this->callback(function ($arg) {
                return $arg instanceof FtpUser
                    && $arg->username === 'testuser';
            }), 'password123')
            ->willReturn(true);

        // Act
        $result = $this->service->execute(1, 'testuser', 'password123', '/opt/novapanel/sites/test');

        // Assert
        $this->assertInstanceOf(FtpUser::class, $result);
        $this->assertEquals('testuser', $result->username);
    }

    public function testExecuteRollsBackOnInfrastructureFailure(): void
    {
        // Mock user exists
        $user = $this->createMock(User::class);
        $this->userRepoMock->method('find')->willReturn($user);

        // Mock FTP username is available
        $this->ftpUserRepoMock->method('findByUsername')->willReturn(null);

        // Mock FTP user creation
        $ftpUser = new FtpUser(
            userId: 1,
            username: 'testuser',
            homeDirectory: '/opt/novapanel/sites/test',
            enabled: true,
            id: 1
        );
        $this->ftpUserRepoMock->method('create')->willReturn($ftpUser);

        // Mock FTP manager fails
        $this->ftpManagerMock->expects($this->once())
            ->method('createUser')
            ->willThrowException(new \Exception('Pure-FTPd error'));

        // Expect rollback (deletion from repository)
        $this->ftpUserRepoMock->expects($this->once())
            ->method('delete')
            ->with(1);

        $this->expectException(\RuntimeException::class);
        $this->expectExceptionMessage('Failed to create FTP user infrastructure');

        // Act
        $this->service->execute(1, 'testuser', 'password123', '/opt/novapanel/sites/test');
    }

    public function testValidFtpUsernameFormats(): void
    {
        // Test various valid username formats
        $validUsernames = [
            'abc',           // minimum length (3 chars)
            'testuser',      // alphanumeric
            'test_user',     // with underscore
            'test-user',     // with hyphen
            'test123',       // alphanumeric with numbers
            str_repeat('a', 32), // maximum length (32 chars)
        ];

        $user = $this->createMock(User::class);
        $this->userRepoMock->method('find')->willReturn($user);
        $this->ftpUserRepoMock->method('findByUsername')->willReturn(null);

        $ftpUser = new FtpUser(
            userId: 1,
            username: 'dummy',
            homeDirectory: '/opt/novapanel/sites/test',
            enabled: true,
            id: 1
        );
        $this->ftpUserRepoMock->method('create')->willReturn($ftpUser);
        $this->ftpManagerMock->method('createUser')->willReturn(true);

        foreach ($validUsernames as $username) {
            try {
                $this->service->execute(1, $username, 'password', '/opt/novapanel/sites/test');
                $this->assertTrue(true); // Username was accepted
            } catch (\InvalidArgumentException $e) {
                $this->fail("Valid username '{$username}' was rejected: " . $e->getMessage());
            }
        }
    }

    public function testInvalidFtpUsernameFormats(): void
    {
        $invalidUsernames = [
            'ab',            // too short (< 3 chars)
            'test user',     // space not allowed
            'test@user',     // @ not allowed
            'test.user',     // dot not allowed
            str_repeat('a', 33), // too long (> 32 chars)
        ];

        foreach ($invalidUsernames as $username) {
            try {
                $this->service->execute(1, $username, 'password', '/opt/novapanel/sites/test');
                $this->fail("Invalid username '{$username}' was accepted");
            } catch (\InvalidArgumentException $e) {
                $this->assertStringContainsString('Invalid FTP username format', $e->getMessage());
            }
        }
    }
}
