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
}
