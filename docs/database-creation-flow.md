# Database Creation Flow in NovaPanel

## How panel.db is Created

### Quick Answer
The `panel.db` file is **NOT explicitly created** by any specific line in `install.sh`. Instead, it is **automatically created by SQLite** when the migration script connects to the database for the first time.

### Detailed Flow

#### 1. install.sh (Line 204)
```bash
sudo -u novapanel php8.2 database/migration.php
```
This line runs the migration script.

#### 2. database/migration.php (Line 11)
```php
$db = Database::panel();
```
This calls the `Database::panel()` static method.

#### 3. app/Infrastructure/Database.php (Lines 32-43)
```php
$dbPath = __DIR__ . '/../../storage/panel.db';
$storageDir = dirname($dbPath);

// Ensure storage directory exists
if (!is_dir($storageDir)) {
    if (!mkdir($storageDir, 0755, true)) {
        throw new \RuntimeException("Failed to create storage directory: $storageDir");
    }
}

try {
    // THIS LINE CREATES panel.db automatically
    self::$panelConnection = new PDO("sqlite:$dbPath");
    // ... rest of the configuration
```

#### 4. SQLite PDO Behavior
When you create a new PDO connection to a SQLite database:
```php
new PDO("sqlite:/path/to/database.db");
```

SQLite will **automatically create the database file** if:
- The file doesn't exist
- The directory exists
- The process has write permissions

### What install.sh Actually Does

1. **Line 196**: Creates storage subdirectories (logs, cache, uploads, etc.)
   ```bash
   mkdir -p storage/logs storage/cache storage/uploads storage/terminal/pids storage/terminal/logs
   ```

2. **Line 197-198**: Sets ownership and permissions
   ```bash
   chown -R novapanel:www-data storage
   chmod -R 775 storage
   ```

3. **Line 204**: Runs migration which triggers panel.db creation
   ```bash
   sudo -u novapanel php8.2 database/migration.php
   ```

4. **Lines 206-209**: After migration, adjusts panel.db permissions
   ```bash
   if [ -f storage/panel.db ]; then
       chown novapanel:www-data storage/panel.db
       chmod 660 storage/panel.db
   fi
   ```

### Key Points

- ✅ `panel.db` is created automatically by SQLite's PDO driver
- ✅ The storage directory is created by `Database::panel()` if it doesn't exist
- ✅ The migration script creates the database schema (tables, indexes, etc.)
- ✅ No manual `touch` or `sqlite3` command is needed to create the file

### Verification

You can test this behavior:

```bash
# Remove storage directory
rm -rf storage

# Run migration
php8.2 database/migration.php

# Check that panel.db was created
ls -lh storage/panel.db
# Output: -rw-r--r-- 1 novapanel www-data 104K Nov 18 03:32 storage/panel.db
```

### Why This Design?

1. **Simplicity**: No need for separate database creation step
2. **Reliability**: SQLite handles file creation atomically
3. **Portability**: Works consistently across all platforms
4. **Error Handling**: PDO will throw clear exceptions if there are permission issues
