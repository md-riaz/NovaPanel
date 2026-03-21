<?php

namespace Tests\Unit\Infrastructure\Adapters;

use App\Infrastructure\Adapters\TerminalAdapter;
use App\Infrastructure\Shell\Shell;
use App\Repositories\TerminalSessionRepository;
use App\Domain\Entities\TerminalSession;
use PHPUnit\Framework\TestCase;

class TerminalAdapterTest extends TestCase
{
    private Shell $shellMock;
    private TerminalSessionRepository $repoMock;
    private TerminalAdapter $adapter;

    protected function setUp(): void
    {
        $this->shellMock = $this->createMock(Shell::class);
        $this->repoMock  = $this->createMock(TerminalSessionRepository::class);
        $this->adapter   = new TerminalAdapter($this->shellMock, $this->repoMock);
    }

    // -------------------------------------------------------------------------
    // isSessionActive
    // -------------------------------------------------------------------------

    public function testIsSessionActiveReturnsFalseWhenNoSessionInDb(): void
    {
        $this->repoMock->expects($this->once())
            ->method('findActiveByUserId')
            ->with(1)
            ->willReturn(null);

        $this->assertFalse($this->adapter->isSessionActive(1));
    }

    public function testIsSessionActiveReturnsFalseWhenSessionExpired(): void
    {
        $session = new TerminalSession(
            id: 'test-uuid',
            userId: 1,
            role: 'Developer',
            ttydPort: 7100,
            processId: 12345,
            status: 'active',
            expiresAt: date('Y-m-d H:i:s', time() - 60),   // expired 1 minute ago
            lastSeenAt: date('Y-m-d H:i:s', time() - 30),
        );

        $this->repoMock->expects($this->once())
            ->method('findActiveByUserId')
            ->willReturn($session);

        $this->assertFalse($this->adapter->isSessionActive(1));
    }

    public function testIsSessionActiveReturnsFalseWhenSessionIdle(): void
    {
        $session = new TerminalSession(
            id: 'test-uuid',
            userId: 1,
            role: 'Developer',
            ttydPort: 7100,
            processId: 12345,
            status: 'active',
            expiresAt: date('Y-m-d H:i:s', time() + 600),
            lastSeenAt: date('Y-m-d H:i:s', time() - TerminalAdapter::IDLE_TIMEOUT - 60),
        );

        $this->repoMock->expects($this->once())
            ->method('findActiveByUserId')
            ->willReturn($session);

        $this->assertFalse($this->adapter->isSessionActive(1));
    }

    // -------------------------------------------------------------------------
    // stopSession
    // -------------------------------------------------------------------------

    public function testStopSessionReturnsFalseWhenNoActiveSession(): void
    {
        $this->repoMock->expects($this->once())
            ->method('findActiveByUserId')
            ->with(42)
            ->willReturn(null);

        $this->assertFalse($this->adapter->stopSession(42));
    }

    public function testStopSessionMarksSessionEndedInDb(): void
    {
        $session = new TerminalSession(
            id: 'abc-123',
            userId: 5,
            role: 'Admin',
            ttydPort: 7101,
            processId: null,   // no OS process → killProcess is a no-op
            status: 'active',
            expiresAt: date('Y-m-d H:i:s', time() + 600),
            lastSeenAt: date('Y-m-d H:i:s', time() - 10),
            createdAt: date('Y-m-d H:i:s', time() - 120),
        );

        $this->repoMock->expects($this->once())
            ->method('findActiveByUserId')
            ->with(5)
            ->willReturn($session);

        $this->repoMock->expects($this->once())
            ->method('markEnded')
            ->with('abc-123');

        $this->assertTrue($this->adapter->stopSession(5));
    }

    // -------------------------------------------------------------------------
    // getSessionInfo
    // -------------------------------------------------------------------------

    public function testGetSessionInfoReturnsNullWhenNoSession(): void
    {
        $this->repoMock->method('findActiveByUserId')->willReturn(null);
        $this->assertNull($this->adapter->getSessionInfo(1));
    }

    public function testGetSessionInfoReturnsArrayForValidSession(): void
    {
        $session = new TerminalSession(
            id: 'aaaa-bbbb-cccc',
            userId: 2,
            role: 'Admin',
            ttydPort: 7100,
            processId: null,
            status: 'active',
            expiresAt: date('Y-m-d H:i:s', time() + 600),
            lastSeenAt: date('Y-m-d H:i:s', time() - 10),
        );

        $this->repoMock->method('findActiveByUserId')->willReturn($session);

        $info = $this->adapter->getSessionInfo(2);

        $this->assertIsArray($info);
        $this->assertSame('aaaa-bbbb-cccc', $info['session_id']);
        $this->assertSame(2, $info['user_id']);
        $this->assertSame('Admin', $info['role']);
        $this->assertSame(7100, $info['port']);
        $this->assertStringContainsString('aaaa-bbbb-cccc', $info['url']);
    }

    // -------------------------------------------------------------------------
    // isTtydInstalled (basic smoke test)
    // -------------------------------------------------------------------------

    public function testIsTtydInstalledReturnsBool(): void
    {
        // We cannot control which ttyd is or isn't installed; just ensure
        // the method returns a boolean without throwing.
        $result = $this->adapter->isTtydInstalled();
        $this->assertIsBool($result);
    }

    // -------------------------------------------------------------------------
    // updateSessionActivity
    // -------------------------------------------------------------------------

    public function testUpdateSessionActivityDoesNothingWhenNoSession(): void
    {
        $this->repoMock->expects($this->once())
            ->method('findActiveByUserId')
            ->willReturn(null);

        // updateLastSeen must NOT be called
        $this->repoMock->expects($this->never())
            ->method('updateLastSeen');

        $this->adapter->updateSessionActivity(99);
    }

    public function testUpdateSessionActivityCallsUpdateLastSeen(): void
    {
        $session = new TerminalSession(
            id: 'sess-xyz',
            userId: 3,
            role: 'Developer',
            ttydPort: 7102,
            processId: null,
            status: 'active',
            expiresAt: date('Y-m-d H:i:s', time() + 600),
            lastSeenAt: date('Y-m-d H:i:s', time() - 5),
        );

        $this->repoMock->expects($this->once())
            ->method('findActiveByUserId')
            ->willReturn($session);

        $this->repoMock->expects($this->once())
            ->method('updateLastSeen')
            ->with('sess-xyz');

        $this->adapter->updateSessionActivity(3);
    }

    // -------------------------------------------------------------------------
    // cleanupStaleSessions
    // -------------------------------------------------------------------------

    public function testCleanupStaleSessionsTerminatesExpiredSessions(): void
    {
        $expired = new TerminalSession(
            id: 'expired-session',
            userId: 10,
            role: 'ReadOnly',
            ttydPort: 7103,
            processId: null,
            status: 'active',
            expiresAt: date('Y-m-d H:i:s', time() - 60),
            lastSeenAt: date('Y-m-d H:i:s', time() - 30),
        );

        $this->repoMock->expects($this->once())
            ->method('findExpired')
            ->willReturn([$expired]);

        $this->repoMock->expects($this->once())
            ->method('findIdleSince')
            ->willReturn([]);

        $this->repoMock->expects($this->once())
            ->method('markEnded')
            ->with('expired-session');

        $this->repoMock->expects($this->once())
            ->method('deleteEnded');

        $count = $this->adapter->cleanupStaleSessions(TerminalAdapter::IDLE_TIMEOUT);
        $this->assertSame(1, $count);
    }

    // -------------------------------------------------------------------------
    // getInstallationInstructions
    // -------------------------------------------------------------------------

    public function testGetInstallationInstructionsReturnsString(): void
    {
        $instructions = $this->adapter->getInstallationInstructions();
        $this->assertIsString($instructions);
        $this->assertStringContainsString('ttyd', $instructions);
    }
}
