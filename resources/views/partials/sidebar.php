<nav class="col-md-3 col-lg-2 d-md-block bg-light sidebar">
    <div class="position-sticky pt-3">
        <ul class="nav flex-column">
            <li class="nav-item">
                <a class="nav-link" href="/dashboard">
                    <i class="bi bi-speedometer2"></i> Dashboard
                </a>
            </li>
            <li class="nav-item">
                <a class="nav-link" href="/users">
                    <i class="bi bi-person-badge"></i> Panel Users
                </a>
            </li>
            <li class="nav-item">
                <a class="nav-link" href="/sites">
                    <i class="bi bi-globe"></i> Sites
                </a>
            </li>
            <li class="nav-item">
                <a class="nav-link" href="/dns">
                    <i class="bi bi-hdd-network"></i> DNS
                </a>
            </li>
            <li class="nav-item">
                <a class="nav-link" href="/ftp">
                    <i class="bi bi-folder2-open"></i> FTP
                </a>
            </li>
            <li class="nav-item">
                <a class="nav-link" href="/databases">
                    <i class="bi bi-database"></i> Databases
                </a>
            </li>
            <li class="nav-item">
                <a class="nav-link" href="/cron">
                    <i class="bi bi-clock"></i> Cron Jobs
                </a>
            </li>
        </ul>
    </div>
</nav>

<style>
.sidebar {
    min-height: calc(100vh - 56px);
}
.sidebar .nav-link {
    color: #333;
}
.sidebar .nav-link:hover {
    background-color: #e9ecef;
}
.sidebar .nav-link.active {
    background-color: #0d6efd;
    color: white;
}
</style>
