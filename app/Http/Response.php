<?php

namespace App\Http;

class Response
{
    public function __construct(
        private string $content = '',
        private int $statusCode = 200,
        private array $headers = []
    ) {}

    public function send(): void
    {
        http_response_code($this->statusCode);

        foreach ($this->headers as $name => $value) {
            header("$name: $value");
        }

        echo $this->content;
    }

    public function json(array $data, int $statusCode = 200): self
    {
        $this->content = json_encode($data);
        $this->statusCode = $statusCode;
        $this->headers['Content-Type'] = 'application/json';
        return $this;
    }

    public function html(string $content, int $statusCode = 200): self
    {
        $this->content = $content;
        $this->statusCode = $statusCode;
        $this->headers['Content-Type'] = 'text/html';
        return $this;
    }

    public function redirect(string $url, int $statusCode = 302): self
    {
        $this->statusCode = $statusCode;
        $this->headers['Location'] = $url;
        return $this;
    }
}
