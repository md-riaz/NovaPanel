<?php

namespace App\Contracts;

use App\Domain\Entities\Database;
use App\Domain\Entities\DatabaseUser;

interface DatabaseManagerInterface
{
    /**
     * Create a new database
     *
     * @param Database $database
     * @return bool
     */
    public function createDatabase(Database $database): bool;

    /**
     * Delete a database
     *
     * @param Database $database
     * @return bool
     */
    public function deleteDatabase(Database $database): bool;

    /**
     * Create a database user
     *
     * @param DatabaseUser $user
     * @param string $password
     * @return bool
     */
    public function createUser(DatabaseUser $user, string $password): bool;

    /**
     * Delete a database user
     *
     * @param DatabaseUser $user
     * @return bool
     */
    public function deleteUser(DatabaseUser $user): bool;

    /**
     * Grant privileges to a user on a database
     *
     * @param DatabaseUser $user
     * @param Database $database
     * @param array $privileges
     * @return bool
     */
    public function grantPrivileges(DatabaseUser $user, Database $database, array $privileges): bool;
}
