# phpMyAdmin SSO Architecture

## Design Decision: Controller-Based Approach

### Why Use a Controller Instead of Standalone Script?

The phpMyAdmin SSO implementation follows NovaPanel's MVC architecture pattern:

**Before (Standalone Script):**
- File: `public/phpmyadmin-signon.php`
- Accessed directly via web server
- Bypasses the router and middleware system
- Inconsistent with rest of the application

**After (Controller-Based):**
- Controller: `App\Http\Controllers\PhpMyAdminController`
- Route: `/phpmyadmin/signon`
- Proper authentication via `AuthMiddleware`
- Consistent with NovaPanel's architecture

### Benefits of Controller Approach:

1. **Consistent Architecture**: Follows the same pattern as other features (databases, sites, users)
2. **Middleware Support**: Automatically applies authentication and other middleware
3. **Testability**: Controllers are easier to unit test
4. **Maintainability**: All business logic in one place with other controllers
5. **Router Integration**: Works seamlessly with the existing routing system
6. **Error Handling**: Benefits from centralized error handling
7. **Request/Response Objects**: Uses framework's request/response abstraction

### Implementation Details:

**Route Definition (public/index.php):**
```php
$router->get('/phpmyadmin/signon', PhpMyAdminController::class . '@signon', [AuthMiddleware::class]);
```

**Controller Method:**
```php
public function signon(Request $request): Response
{
    // Load environment configuration
    // Get MySQL credentials
    // Set phpMyAdmin signon session
    // Redirect to phpMyAdmin
    return $this->redirect($redirectUrl);
}
```

**Benefits:**
- ✅ Protected by AuthMiddleware (user must be logged in)
- ✅ Uses Request object for clean parameter access
- ✅ Returns Response object for consistent behavior
- ✅ Inherits from base Controller for shared functionality
- ✅ Follows PSR-4 autoloading standards

### Files Modified:

1. **Created**: `app/Http/Controllers/PhpMyAdminController.php`
2. **Updated**: `public/index.php` (added route and import)
3. **Updated**: `resources/views/pages/databases/index.php` (URL changed)
4. **Updated**: `resources/views/partials/sidebar.php` (URL changed)
5. **Updated**: `install.sh` (SignonURL in config)
6. **Removed**: `public/phpmyadmin-signon.php` (old standalone script)

This change ensures NovaPanel maintains a clean, consistent architecture throughout the application.
