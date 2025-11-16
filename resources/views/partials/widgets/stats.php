<div class="col-md-3 mb-3">
    <div class="card text-white bg-primary">
        <div class="card-body">
            <h5 class="card-title">Accounts</h5>
            <p class="card-text display-4"><?= $stats['accounts'] ?? 0 ?></p>
        </div>
    </div>
</div>
<div class="col-md-3 mb-3">
    <div class="card text-white bg-success">
        <div class="card-body">
            <h5 class="card-title">Sites</h5>
            <p class="card-text display-4"><?= $stats['sites'] ?? 0 ?></p>
        </div>
    </div>
</div>
<div class="col-md-3 mb-3">
    <div class="card text-white bg-info">
        <div class="card-body">
            <h5 class="card-title">Databases</h5>
            <p class="card-text display-4"><?= $stats['databases'] ?? 0 ?></p>
        </div>
    </div>
</div>
<div class="col-md-3 mb-3">
    <div class="card text-white bg-warning">
        <div class="card-body">
            <h5 class="card-title">FTP Users</h5>
            <p class="card-text display-4"><?= $stats['ftp_users'] ?? 0 ?></p>
        </div>
    </div>
</div>
