// Run with node internal/web/site_delete_result_test.cjs
// IDs are now validated and rendered on the server; the Go HTTP test checks
// visible markup and rejection of hostile IDs without relying on JavaScript.
const assert = require('node:assert/strict');
const fs = require('node:fs');
const template = fs.readFileSync('internal/web/templates/console.html', 'utf8');
assert.match(template, /data-attached-databases>\{\{range \.attached_database_ids\}\}<li>\{\{\.\}\}<\/li>/);
assert.doesNotMatch(template, /hidden data-database-id/);
console.log('site delete IDs do not require JavaScript');
