<!DOCTYPE html>
<html lang="en">
<head>
    <meta charset="UTF-8">
    <meta name="viewport" content="width=device-width, initial-scale=1.0">
    <title><?= $title ?? 'NovaPanel' ?></title>
    <link href="/assets/css/bootstrap.min.css" rel="stylesheet">
    <link href="/assets/css/bootstrap-icons.css" rel="stylesheet">
    <style>
        .htmx-indicator {
            display: none;
        }
        .htmx-request .htmx-indicator {
            display: inline-block;
        }
        .htmx-request.htmx-indicator {
            display: inline-block;
        }
    </style>
</head>
<body>
    <?php include __DIR__ . '/../partials/navbar.php'; ?>
    
    <div class="container-fluid">
        <div class="row">
            <?php include __DIR__ . '/../partials/sidebar.php'; ?>
            
            <main class="col-md-9 ms-sm-auto col-lg-10 px-md-4">
                <div class="py-4">
                    <?php if (isset($content)) echo $content; ?>
                </div>
            </main>
        </div>
    </div>

    <script src="/assets/js/bootstrap.bundle.min.js"></script>
    <script src="/assets/js/htmx.min.js"></script>
    <script>
        // Configure HTMX
        document.body.addEventListener('htmx:configRequest', function(evt) {
            // Add CSRF token if available
            const csrfToken = document.querySelector('meta[name="csrf-token"]');
            if (csrfToken) {
                evt.detail.headers['X-CSRF-Token'] = csrfToken.content;
            }
        });

        // Global error handler
        document.body.addEventListener('htmx:responseError', function(evt) {
            alert('An error occurred: ' + (evt.detail.xhr.statusText || 'Unknown error'));
        });

        // Show success messages
        document.body.addEventListener('htmx:afterSwap', function(evt) {
            if (evt.detail.successful && evt.detail.target.hasAttribute('data-success-message')) {
                const message = evt.detail.target.getAttribute('data-success-message');
                // Create a temporary alert
                const alert = document.createElement('div');
                alert.className = 'alert alert-success alert-dismissible fade show';
                alert.innerHTML = message + '<button type="button" class="btn-close" data-bs-dismiss="alert"></button>';
                evt.detail.target.insertBefore(alert, evt.detail.target.firstChild);
                setTimeout(() => alert.remove(), 3000);
            }
        });
    </script>
</body>
</html>
