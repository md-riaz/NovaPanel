<?php

namespace App\Http\Controllers;

use App\Http\Response;

abstract class Controller
{
    protected function view(string $template, array $data = []): Response
    {
        $viewPath = __DIR__ . '/../../../resources/views/' . $template . '.php';
        
        if (!file_exists($viewPath)) {
            return new Response("View not found: $template", 404);
        }

        ob_start();
        extract($data);
        require $viewPath;
        $content = ob_get_clean();

        return (new Response())->html($content);
    }

    protected function json(array $data, int $statusCode = 200): Response
    {
        return (new Response())->json($data, $statusCode);
    }

    protected function redirect(string $url): Response
    {
        return (new Response())->redirect($url);
    }

    /**
     * Generate success alert HTML for HTMX responses
     */
    protected function successAlert(string $message): string
    {
        $html = '<div class="alert alert-success alert-dismissible fade show" role="alert">';
        $html .= '<i class="bi bi-check-circle"></i> ' . htmlspecialchars($message);
        $html .= '<button type="button" class="btn-close" data-bs-dismiss="alert"></button>';
        $html .= '</div>';
        return $html;
    }

    /**
     * Generate error alert HTML for HTMX responses
     */
    protected function errorAlert(string $message): string
    {
        $html = '<div class="alert alert-danger alert-dismissible fade show" role="alert">';
        $html .= '<i class="bi bi-exclamation-triangle"></i> Error: ' . htmlspecialchars($message);
        $html .= '<button type="button" class="btn-close" data-bs-dismiss="alert"></button>';
        $html .= '</div>';
        return $html;
    }
}
