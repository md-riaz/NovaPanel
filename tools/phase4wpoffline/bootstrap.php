<?php
// Lab-only bootstrap, fixed early require rather than Composer double loading.
const LAB_PHAR = '/lab/cli.phar';
const LAB_URL = 'https://api.wordpress.org/core/checksums/1.0/?version=7.1.4&locale=en_US';
if (hash_file('sha256', LAB_PHAR) !== 'ce34ddd838f7351d6759068d09793f26755463b4a4610a5a5c0a97b68220d85c'
    || hash_file('sha256', '/lab/checksums.json') !== 'bb718bddf197e0098f05395dd9b228fad70b394926ee9d70a8c6a67cf20781e7') {
    exit(78);
}
putenv('WP_CLI_EARLY_REQUIRE=/lab/transport.php');
require LAB_PHAR;
