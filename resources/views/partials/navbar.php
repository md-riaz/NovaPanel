<nav class="navbar navbar-dark bg-dark sticky-top">
    <div class="container-fluid">
        <a class="navbar-brand" href="/">
            <i class="bi bi-lightning-charge-fill"></i> NovaPanel
        </a>
        <div class="d-flex">
            <span class="navbar-text text-white me-3">
                <i class="bi bi-person-circle"></i> 
                <?php 
                use App\Http\Session;
                use App\Support\CSRF;
                Session::start();
                echo htmlspecialchars(Session::get('username', 'User')); 
                ?>
            </span>
            <form method="POST" action="/logout" style="display: inline;">
                <?php echo CSRF::field(); ?>
                <button type="submit" class="btn btn-sm btn-outline-light">
                    <i class="bi bi-box-arrow-right"></i> Logout
                </button>
            </form>
        </div>
    </div>
</nav>
