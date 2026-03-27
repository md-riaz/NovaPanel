<?php

namespace App\Http;

class Router
{
    private array $routes = [];
    private array $globalMiddleware = [];

    public function get(string $path, $handler, array $middleware = []): void
    {
        $this->addRoute('GET', $path, $handler, $middleware);
    }

    public function post(string $path, $handler, array $middleware = []): void
    {
        $this->addRoute('POST', $path, $handler, $middleware);
    }

    public function put(string $path, $handler, array $middleware = []): void
    {
        $this->addRoute('PUT', $path, $handler, $middleware);
    }

    public function delete(string $path, $handler, array $middleware = []): void
    {
        $this->addRoute('DELETE', $path, $handler, $middleware);
    }

    private function addRoute(string $method, string $path, $handler, array $middleware): void
    {
        $this->routes[] = [
            'method' => $method,
            'path' => $path,
            'handler' => $handler,
            'middleware' => $middleware,
        ];
    }

    public function addGlobalMiddleware(string $middleware): void
    {
        $this->globalMiddleware[] = $middleware;
    }

    public function dispatch(Request $request): Response
    {
        foreach ($this->routes as $route) {
            if ($this->matchRoute($route, $request)) {
                $route['middleware'] = array_merge($this->globalMiddleware, $route['middleware'] ?? []);
                return $this->handleRoute($route, $request);
            }
        }

        return new Response('Not Found', 404);
    }

    private function matchRoute(array $route, Request $request): bool
    {
        if ($route['method'] !== $request->method()) {
            return false;
        }

        $pattern = preg_replace('/\{([^}]+)\}/', '([^/]+)', $route['path']);
        $pattern = '#^' . $pattern . '$#';

        return (bool) preg_match($pattern, $request->path());
    }

    private function handleRoute(array $route, Request $request): Response
    {
        $handler = $route['handler'];
        $middlewares = $route['middleware'] ?? [];

        $next = function ($request) use ($handler, $route) {
            $params = $this->extractParams($route['path'], $request->path());

            if (is_string($handler) && str_contains($handler, '@')) {
                [$controller, $method] = explode('@', $handler);
                $controller = new $controller();

                if (!empty($params)) {
                    return $controller->$method($request, ...$params);
                }

                return $controller->$method($request);
            }

            if (is_callable($handler)) {
                return $handler($request);
            }

            return new Response('Handler not found', 500);
        };

        foreach (array_reverse($middlewares) as $middleware) {
            [$middlewareClass, $arguments] = $this->parseMiddleware($middleware);
            $middlewareInstance = new $middlewareClass(...$arguments);
            $next = function ($request) use ($middlewareInstance, $next) {
                return $middlewareInstance->handle($request, $next);
            };
        }

        return $next($request);
    }

    private function parseMiddleware(string $middleware): array
    {
        if (!str_contains($middleware, ':')) {
            return [$middleware, []];
        }

        [$middlewareClass, $argumentList] = explode(':', $middleware, 2);
        $arguments = $argumentList === '' ? [] : array_map('trim', explode(',', $argumentList));

        return [$middlewareClass, $arguments];
    }

    private function extractParams(string $routePath, string $requestPath): array
    {
        $routeParts = explode('/', trim($routePath, '/'));
        $requestParts = explode('/', trim($requestPath, '/'));
        $params = [];

        foreach ($routeParts as $index => $part) {
            if (preg_match('/\{([^}]+)\}/', $part, $matches)) {
                $params[] = $requestParts[$index] ?? null;
            }
        }

        return $params;
    }
}
