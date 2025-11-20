<?php
session_start();
if (!isset($_SESSION['user_id'])) {
    http_response_code(401);
    exit;
}
// Optional: you can check roles here:
// if ($_SESSION['role'] !== 'admin') { http_response_code(403); exit; }
http_response_code(200);
