# 03 — Verb arg/reply shapes

The args struct and reply shape for every v1 verb. Complements `01-rpc-and-verbs.md` (which fixes each
verb's Role/Scope/Slow/Effectful/MFA). A verb's phase spec must match this file; the implementer writes
the `NewArgs` struct exactly as listed and validates fields (F9) before `Run`.

**Conventions (stated once):**
- **Reads** return `data` = the shape shown. **Slow verbs** return `data:{job:{id,state,verb}}`
  immediately (F6); the real result lands in `jobs.result_json` (shape = the "→result" shown) and is
  read via `job.status`. **Show-once secrets** ride the `claim` (F19), never the result.
- All ids are ULIDs (`site_…`, `db_…`, `job_…`, etc. — opaque). Timestamps are unix-ms. Field names
  are snake_case. Unlisted optional fields default as noted. Every string that is a name/path/url/host
  is validated per F9.
- Omitted here: the universal envelope (F2) and `job.*`/error plumbing.

---

## session / mfa / user (Phase 2)
```
session.start{}                                        → {session, csrf_token}   # public pre-login bootstrap; idempotent get-or-create: live anon in F2 → same session+csrf unchanged (lets web re-fetch anon csrf for the login-POST compare); none/expired → mints (rate-limited per peer; exhausted → forbidden); authed session → precondition_failed (F3; use whoami)
session.login{user, password, totp?:string}            → {session, role, csrf_token, mfa:bool}
session.reauth{totp}                                    → {session, csrf_token, mfa_at}   # rotates the session id + csrf (F4); web replaces its cookie/header
session.logout{}                                        → {}
session.whoami{}                                        → {user, role, csrf_token, mfa_at, scopes?}
mfa.enroll{}                                            → {otpauth_uri}        # shown once
mfa.confirm{totp}                                       → {backup_codes:[string]}  # shown once
mfa.disable{totp}                                       → {}
user.create{username, email, role, password}            → {user_id}
user.update{user_id, email?, role?, disabled?}          → {}
user.set_password{user_id, password}                    → {}
user.delete{user_id}                                    → {}
user.list{}                                             → {users:[{id,username,email,role,disabled}]}
tokens.create{name, scopes:[string], expires?:int, automation?:bool} → {token}   # npat_… shown once
tokens.list{}                                           → {tokens:[{id,name,scopes,automation,expires,last_used,revoked}]}
tokens.revoke{token_id}                                 → {}
```

## core / jobs / system (Phase 0–1)
```
ping{}                                                  → {version}
agent.info{}                                            → {version, build, started, uptime_sec}
job.status{id}                                          → {id,verb,state,step,progress,result?,error?,code?,events:[{ts,msg}]}
job.list{state?, verb?, limit?:int=50, before?:string}  → {jobs:[…], next?:string}
job.claim{job_id, claim}                                → {<secret fields>}   # one-shot (F19)
job.cancel{id}                                          → {}
system.status{}                                         → {services:[{unit,active}], load, uptime_sec}
system.metrics{}                                        → {cpu_pct, mem:{used,total}, disk:[{mount,used,total}]}
service.status{unit}                                    → {unit, active:bool, enabled:bool, state:string}
service.restart{unit}                                   → job → {unit, restarted_at}
schedule.list{}                                         → {schedules:[{id,verb,cron,next_run,enabled,last_run}]}
schedule.set{id?, verb, args, cron, enabled?}           → {id}
schedule.enable{id}/schedule.disable{id}                → {}
update.check{}                                          → {current, latest?, security_fix?:bool, notes?}
update.apply{}                                          → job → {from, to}
reconcile{scope?:string}                                → job → {changed:[string]}
audit.tail{limit?:int=200, before?:string}              → {events:[{ts,actor,role,verb,object_key,outcome,code}], next?}
panel.backup_config{repo_id}                            → job → {snapshot_id}
```

## sites / domains / ssl / php (Phase 3)
```
site.create{name?, type:'static'|'php'|'wordpress', primary_domain, php_version?} → job → {site_id}
                                                        # name generated from the domain if omitted
site.update{site_id, php_version?, force_https?:bool, hsts_max_age?:int, maintenance?:bool} → job → {}
site.delete{site_id, purge_files?:bool}                 → job → {}      # MFA; removes user/docroot/certs/DB rows per flags
site.list{}                                            → {sites:[{id,name,type,primary_domain,status,php_version}]}
site.get{site_id}                                       → {site:{…}, domains:[…], redirects:[…], cert:{state,not_after}}
domain.add{site_id, fqdn}                               → job → {domain_id}
domain.remove{domain_id}                                → job → {}
ssl.issue{site_id, staging?:bool, wildcard?:bool}       → job → {fqdns:[string], not_after}
ssl.renew{site_id}                                      → job → {not_after}
ssl.upload{site_id, cert_pem, key_pem}                  → job → {fqdns:[string], not_after}  # MFA; validates key↔cert + dates
ssl.auto_set{site_id, enabled:bool}                     → {}        # Let's Encrypt auto-renew on/off
ssl.status{site_id}                                     → {source:'letsencrypt'|'manual'|'none', ssl_auto:bool, force_https:bool, domains:[{fqdn,cert_state,not_after}]}
php.set{site_id, version, limits?:{memory_mb?,upload_mb?,max_exec_sec?}} → job → {}
php.list{}                                              → {versions:[string], default}
redirect.add{site_id, source, target, code:301|302}     → job → {redirect_id}   # source/target validated (F9)
redirect.remove{redirect_id}                            → job → {}
```

## databases (Phase 4)
```
database.create{site_id?, name?}                        → job → {db_id, db_name, db_user}  + claim(password)
database.rotate_password{db_id}                         → job → {}                          + claim(password)
database.delete{db_id}                                  → job → {}      # MFA
database.list{}                                         → {databases:[{id,name,site_id,engine}]}
dbuser.create{db_id, username?}                         → job → {dbuser_id}                 + claim(password)
dbuser.delete{dbuser_id}                                → job → {}
```

## files / deploy / cron / log / ssh (Phase 5)
```
files.list{site_id, path}                               → {entries:[{name,type,size,mode,mtime}]}
files.read{site_id, path, offset?:int, length?:int}     → {content_b64, size, eof:bool}   # length ≤ 512 KiB/chunk
files.write{site_id, path, content_b64}                 → {}                                # temp+atomic, as site user
files.mkdir{site_id, path} / files.delete{site_id, path} / files.move{site_id, from, to} → {}
files.chmod{site_id, path, mode}                        → {}        # mode ∈ {0644,0640,0600,0755,0750,0700}; else invalid_argument
files.upload{site_id, path, upload_id}                  → job → {}  # staged (see upload note)
files.archive{site_id, paths:[string], dest}            → job → {}
files.extract{site_id, archive, dest}                   → job → {}  # archive-entry guard (F9/F15)
deploy.configure{site_id, repo_url, branch}             → {deploy_pubkey}   # public key; also re-readable via deploy.status (not a secret)
deploy.run{site_id, ref}                                → job → {sha}       # ref resolved→pinned sha
deploy.rollback{site_id, sha}                           → job → {sha}
deploy.status{site_id}                                  → {current_sha, state, last_run, deploy_pubkey}
deploy.history{site_id, limit?}                         → {deploys:[{sha,state,created}]}
cron.create{site_id, schedule, argv:[string]}           → job → {cron_id}   # schedule=OnCalendar; argv validated
cron.delete{cron_id}/cron.enable{cron_id}/cron.disable{cron_id} → job → {}
cron.list{site_id}                                      → {jobs:[{id,schedule,argv,enabled}]}
log.tail{site_id?, target, lines?:int=200}              → {lines:[string]}  # target ∈ allowlist
sshkey.add{site_id, public_key}                         → {key_id}          # MFA; public key only, options stripped
sshkey.remove{key_id}                                   → {}                # MFA
sshkey.sftp_only{site_id, enabled:bool}                 → job → {}          # MFA; sshd render+reload
sshkey.list{site_id}                                    → {keys:[{id,comment,type,fingerprint}]}
```
**Upload note:** `files.upload` content does not travel in the 1 MiB RPC frame. The web tier streams
the file to a staging path it owns, gets a `upload_id`, then calls `files.upload{upload_id}`; the agent
moves it into place as the site user. (Large archives/extract run as jobs.)

## backups / firewall (Phase 6)
```
repo.create{name, kind:'local'|'sftp'|'s3', location, role?:'site'|'panel', creds?:{…}} → job → {repo_id}  + claim(password)   # Slow (restic init); creds sealed; one panel repo max (else conflict)
repo.list{}                                            → {repos:[{id,name,kind,location,role}]}
repo.delete{repo_id}                                    → {}       # MFA
backup.create{repo_id, target_kind:'site'|'db'|'panel', target_ref} → job → {snapshot_id}
backup.list{repo_id?, target_ref?}                      → {backups:[{id,snapshot_id,target_kind,target_ref,bytes,created}]}
backup.verify{repo_id, mode?:'check'|'read-data'}       → job → {ok:bool, detail}
backup.restore{backup_id, confirm:string, scope?:'file'|'db'|'full', include?:[string]} → job → {}
                                                        # MFA; confirm must equal the target name (else invalid_argument); pre-restore snapshot taken
firewall.status{}                                       → {enabled:bool, rules:[{num,action,proto,port,source,comment}], pending?:{id,deadline}}
firewall.apply{rules:[{action,proto,port,source?,comment?}], window_min?:int} → job → {pending_id}   # window_min default Cfg.firewall_window_min
                                                        # MFA; conflict if already armed; structured rules only (no raw ufw)
firewall.confirm{pending_id}                            → {}       # MFA; stops the revert timer
firewall.revert{pending_id}                             → {}       # MFA; reverts now
```

## dns (Phase 7)
```
integration.set{kind:'cloudflare', token, zones:[string]} → {integration_id}   # token sealed; MFA
dns.record.create{zone, type, name, value, ttl?:int, priority?:int, proxied?:bool} → job → {record_id}
dns.record.update{zone, record_id, …}                   → job → {}
dns.record.delete{zone, record_id}                      → job → {}
dns.record.list{zone}                                   → {records:[{id,type,name,value,ttl,proxied}]}
dns.verify{domain}                                      → {points_here:bool, method:'resolver'|'provider'}
dns.purge{zone, urls?:[string]}                         → job → {}
```

## modules / webhooks (Phase 8–9)
```
ols.status{}                                            → {installed, sites:[{site_id,enabled,version,status}]}
ols.enable{site_id, confirm:bool, check_only?:bool}     → job → {baseline, result:'enabled'|'checked'|'reverted'}
ols.disable{site_id, confirm:bool}                       → job → {result:'disabled'|'reverted'}
module.install{name}/module.enable{name,target?}/module.disable{name,target?} → job → {}
module.status{name}                                     → {name, enabled, detail}
module.list{}                                           → {modules:[{name,enabled,scope}]}
deploy.hook_create{site_id, branch}                     → {hook_id, secret}   # internal; secret shown once
deploy.hook_list{site_id}/deploy.hook_delete{hook_id}   → {…}/{}
# internal (web-uid peer only): tokens.verify{token}→{token_id,scopes,automation}; deploy.hook{hook_id,body_b64,signature,timestamp}→{} (uniform)
```
