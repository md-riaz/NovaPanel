<?php

namespace Tests\Unit\Infrastructure\Adapters;

use App\Infrastructure\Adapters\MysqlDatabaseAdapter;
use App\Contracts\ShellInterface;
use App\Domain\Entities\Database;
use App\Domain\Entities\DatabaseUser;
use PHPUnit\Framework\TestCase;

class MysqlDatabaseAdapterTest extends TestCase
{
    private ShellInterface $shellMock;

    protected function setUp(): void
    {
        $this->shellMock = $this->createMock(ShellInterface::class);
    }

    public function testSanitizeDatabaseNameRemovesInvalidCharacters(): void
    {
        // This is a test for the private method's behavior
        // We test it indirectly through createDatabase

        $adapter = new MysqlDatabaseAdapter(
            $this->shellMock,
            'localhost',
            'testuser',
            'testpass'
        );

        // Test that special characters are handled
        // We'll use reflection to test the private method
        $reflection = new \ReflectionClass($adapter);
        $method = $reflection->getMethod('sanitizeDatabaseName');
        $method->setAccessible(true);

        $result = $method->invoke($adapter, 'test-db!@#$%^&*()');
        $this->assertEquals('testdb', $result);

        // Test that names starting with numbers are prefixed
        $result = $method->invoke($adapter, '123database');
        $this->assertEquals('db_123database', $result);

        // Test length limitation
        $longName = str_repeat('a', 100);
        $result = $method->invoke($adapter, $longName);
        $this->assertEquals(64, strlen($result));
    }

    public function testSanitizeUsernameRemovesInvalidCharacters(): void
    {
        $adapter = new MysqlDatabaseAdapter(
            $this->shellMock,
            'localhost',
            'testuser',
            'testpass'
        );

        $reflection = new \ReflectionClass($adapter);
        $method = $reflection->getMethod('sanitizeUsername');
        $method->setAccessible(true);

        $result = $method->invoke($adapter, 'user!@#$%^&*()');
        $this->assertEquals('user', $result);

        // Test length limitation
        $longName = str_repeat('a', 50);
        $result = $method->invoke($adapter, $longName);
        $this->assertEquals(32, strlen($result));
    }

    public function testCreateDatabaseThrowsExceptionWhenRootUserNotConfigured(): void
    {
        // Test with invalid credentials to ensure error handling
        $adapter = new MysqlDatabaseAdapter(
            $this->shellMock,
            'localhost',
            '', // Empty user to trigger error
            ''
        );

        $database = new Database(
            userId: 1,
            name: 'testdb',
            type: 'mysql'
        );

        $this->expectException(\RuntimeException::class);
        $this->expectExceptionMessage('MySQL root user is not configured');

        $adapter->createDatabase($database);
    }

    public function testDeleteDatabaseThrowsExceptionWhenRootUserNotConfigured(): void
    {
        $adapter = new MysqlDatabaseAdapter(
            $this->shellMock,
            'localhost',
            '',
            ''
        );

        $database = new Database(
            userId: 1,
            name: 'testdb',
            type: 'mysql'
        );

        $this->expectException(\RuntimeException::class);
        $this->expectExceptionMessage('MySQL root user is not configured');

        $adapter->deleteDatabase($database);
    }

    public function testCreateUserThrowsExceptionWhenRootUserNotConfigured(): void
    {
        $adapter = new MysqlDatabaseAdapter(
            $this->shellMock,
            'localhost',
            '',
            ''
        );

        $user = new DatabaseUser(
            databaseId: 1,
            username: 'testuser',
            host: 'localhost'
        );

        $this->expectException(\RuntimeException::class);
        $this->expectExceptionMessage('MySQL root user is not configured');

        $adapter->createUser($user, 'password123');
    }

    public function testDeleteUserThrowsExceptionWhenRootUserNotConfigured(): void
    {
        $adapter = new MysqlDatabaseAdapter(
            $this->shellMock,
            'localhost',
            '',
            ''
        );

        $user = new DatabaseUser(
            databaseId: 1,
            username: 'testuser',
            host: 'localhost'
        );

        $this->expectException(\RuntimeException::class);
        $this->expectExceptionMessage('MySQL root user is not configured');

        $adapter->deleteUser($user);
    }

    public function testGrantPrivilegesThrowsExceptionWhenRootUserNotConfigured(): void
    {
        $adapter = new MysqlDatabaseAdapter(
            $this->shellMock,
            'localhost',
            '',
            ''
        );

        $user = new DatabaseUser(
            databaseId: 1,
            username: 'testuser',
            host: 'localhost'
        );

        $database = new Database(
            userId: 1,
            name: 'testdb',
            type: 'mysql'
        );

        $this->expectException(\RuntimeException::class);
        $this->expectExceptionMessage('MySQL root user is not configured');

        $adapter->grantPrivileges($user, $database, ['SELECT', 'INSERT']);
    }

    public function testGrantPrivilegesWithEmptyPrivilegesArray(): void
    {
        $adapter = new MysqlDatabaseAdapter(
            $this->shellMock,
            'localhost',
            '',
            ''
        );

        $user = new DatabaseUser(
            databaseId: 1,
            username: 'testuser',
            host: 'localhost'
        );

        $database = new Database(
            userId: 1,
            name: 'testdb',
            type: 'mysql'
        );

        $this->expectException(\RuntimeException::class);
        $this->expectExceptionMessage('MySQL root user is not configured');

        // Empty privileges array should default to ALL PRIVILEGES
        $adapter->grantPrivileges($user, $database, []);
    }
}
