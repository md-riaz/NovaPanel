<?php
/**
 * Script to create an admin user for testing
 */

require_once __DIR__ . '/vendor/autoload.php';

$dbPath = __DIR__ . '/storage/panel.db';

if (!file_exists($dbPath)) {
    echo "❌ Database not found. Please run migration.php first.\n";
    exit(1);
}

$db = new PDO('sqlite:' . $dbPath);
$db->setAttribute(PDO::ATTR_ERRMODE, PDO::ERRMODE_EXCEPTION);

// Check if admin user already exists
$stmt = $db->prepare("SELECT id FROM users WHERE username = ?");
$stmt->execute(['admin']);
if ($stmt->fetch()) {
    echo "✓ Admin user already exists\n";
    echo "Username: admin\n";
    echo "Password: admin123\n";
    exit(0);
}

// Get Admin role ID
$stmt = $db->prepare("SELECT id FROM roles WHERE name = 'Admin'");
$stmt->execute();
$adminRole = $stmt->fetch(PDO::FETCH_ASSOC);

if (!$adminRole) {
    echo "❌ Admin role not found. Please run migration.php first.\n";
    exit(1);
}

// Create admin user
$username = 'admin';
$email = 'admin@novapanel.local';
$password = password_hash('admin123', PASSWORD_DEFAULT);

$stmt = $db->prepare("
    INSERT INTO users (username, email, password, created_at, updated_at)
    VALUES (?, ?, ?, datetime('now'), datetime('now'))
");

try {
    $stmt->execute([$username, $email, $password]);
    $userId = $db->lastInsertId();
    
    // Assign Admin role
    $stmt = $db->prepare("
        INSERT INTO user_roles (user_id, role_id)
        VALUES (?, ?)
    ");
    $stmt->execute([$userId, $adminRole['id']]);
    
    echo "✅ Admin user created successfully!\n";
    echo "━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━\n";
    echo "Username: admin\n";
    echo "Password: admin123\n";
    echo "━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━\n";
    echo "\nYou can now login at http://localhost:7080/login\n";
    
} catch (PDOException $e) {
    echo "❌ Error creating admin user: " . $e->getMessage() . "\n";
    exit(1);
}
