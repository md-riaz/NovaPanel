# Static Regression Checklist

This checklist covers every sidebar menu destination and every action button rendered from the current page templates so dead links can be caught during route/view consistency passes.

## Sidebar menu items

- [ ] `/dashboard` loads the dashboard page.
- [ ] `/users` loads the panel users index.
- [ ] `/sites` loads the sites index.
- [ ] `/dns` loads the DNS zones index.
- [ ] `/ftp` loads the FTP users index.
- [ ] `/databases` loads the databases index.
- [ ] `/phpmyadmin/signon` opens phpMyAdmin sign-on.
- [ ] `/cron` loads the cron jobs index.
- [ ] `/terminal` loads the terminal page.

## Global navigation actions

- [ ] Navbar brand `/` resolves to the dashboard.
- [ ] Logout submits `POST /logout`.

## Dashboard actions

- [ ] Refresh Stats issues `GET /dashboard/stats`.
- [ ] Create New User links to `/users/create`.
- [ ] Create New Site links to `/sites/create`.
- [ ] Create New Database links to `/databases/create`.

## Users page actions

- [ ] Create User links to `/users/create`.
- [ ] Edit uses `/users/{id}/edit`.
- [ ] Delete submits `POST /users/{id}/delete`.
- [ ] Create User cancel links back to `/users`.
- [ ] Edit User cancel links back to `/users`.
- [ ] Edit User form submits `POST /users/{id}`.
- [ ] Create User form submits `POST /users`.

## Sites page actions

- [ ] Create Site links to `/sites/create`.
- [ ] Sites index does not render unsupported per-site show/edit/delete actions.
- [ ] Create Site cancel links back to `/sites`.
- [ ] Create Site form submits `POST /sites`.

## DNS page actions

- [ ] Create DNS Zone links to `/dns/create`.
- [ ] Every DNS zone row links to `/dns/{id}`.
- [ ] DNS Zone create cancel links back to `/dns`.
- [ ] DNS Zone create form submits `POST /dns`.
- [ ] DNS records page back button links to `/dns`.
- [ ] Add record form submits `POST /dns/{id}/records`.
- [ ] Delete record submits `POST /dns/{domainId}/records/{recordId}/delete`.

## FTP page actions

- [ ] Create FTP User links to `/ftp/create`.
- [ ] Delete FTP User submits `POST /ftp/{id}/delete` with confirmation.
- [ ] Create FTP User cancel links back to `/ftp`.
- [ ] Create FTP User form submits `POST /ftp`.

## Database page actions

- [ ] phpMyAdmin button links to `/phpmyadmin/signon`.
- [ ] Create Database links to `/databases/create`.
- [ ] Row-level Manage button links to `/phpmyadmin/signon?db={name}`.
- [ ] Row-level Delete submits `POST /databases/{id}/delete`.
- [ ] Create Database cancel links back to `/databases`.
- [ ] Create Database form submits `POST /databases`.

## Cron page actions

- [ ] Create Cron Job links to `/cron/create`.
- [ ] Delete Cron Job submits `POST /cron/{id}/delete`.
- [ ] Create Cron Job cancel links back to `/cron`.
- [ ] Create Cron Job form submits `POST /cron`.

## Terminal page actions

- [ ] `/terminal` route renders a page or install/error state without 404s.
- [ ] Terminal control actions submit to `/terminal/start`, `/terminal/stop`, and `/terminal/restart`.
- [ ] Terminal status polling hits `/terminal/status`.
- [ ] Terminal install/error fallback buttons link back to `/dashboard`.
