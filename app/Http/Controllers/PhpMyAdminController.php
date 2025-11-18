<?php

namespace App\Http\Controllers;

use App\Http\Request;
use App\Http\Response;

class PhpMyAdminController extends Controller
{
    /**
     * Handle phpMyAdmin SSO (Single Sign-On)
     * 
     * This method sets up the phpMyAdmin signon session and redirects to phpMyAdmin.
     * The user is automatically authenticated without entering credentials.
     */
    public function signon(Request $request): Response
    {
        // Load environment configuration
        $envFile = __DIR__ . '/../../../.env.php';
        if (file_exists($envFile)) {
            require_once $envFile;
        }
        
        // Get MySQL credentials from environment
        $mysqlHost = getenv('MYSQL_HOST') ?: 'localhost';
        $mysqlUser = getenv('MYSQL_ROOT_USER') ?: 'root';
        $mysqlPassword = getenv('MYSQL_ROOT_PASSWORD') ?: '';
        
        // Set phpMyAdmin signon session with MySQL credentials
        $_SESSION['novapanel_pma_signon'] = [
            'user' => $mysqlUser,
            'password' => $mysqlPassword,
            'host' => $mysqlHost,
        ];
        
        // Build redirect URL with optional database parameter
        $redirectUrl = '/phpmyadmin/';
        $db = $request->get('db');
        if ($db && !empty($db)) {
            $redirectUrl .= '?db=' . urlencode($db);
        }
        
        // Redirect to phpMyAdmin
        return $this->redirect($redirectUrl);
    }
}
