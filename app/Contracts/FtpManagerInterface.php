<?php

namespace App\Contracts;

use App\Domain\Entities\FtpUser;

interface FtpManagerInterface
{
    /**
     * Create a new FTP user
     *
     * @param FtpUser $user
     * @param string $password
     * @return bool
     */
    public function createUser(FtpUser $user, string $password): bool;

    /**
     * Update an FTP user
     *
     * @param FtpUser $user
     * @return bool
     */
    public function updateUser(FtpUser $user): bool;

    /**
     * Delete an FTP user
     *
     * @param FtpUser $user
     * @return bool
     */
    public function deleteUser(FtpUser $user): bool;

    /**
     * Change FTP user password
     *
     * @param FtpUser $user
     * @param string $password
     * @return bool
     */
    public function changePassword(FtpUser $user, string $password): bool;
}
