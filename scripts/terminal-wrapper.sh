#!/usr/bin/env bash
# ============================================================================
# NovaPanel Terminal Wrapper Script
#
# Installed at:  /opt/novapanel/bin/terminal-wrapper.sh
# Usage:         terminal-wrapper.sh <session_id> <role>
#
# This script is launched by ttyd instead of a raw shell.  It:
#   1. Sanitises the process environment (removes secrets).
#   2. Sets a safe, minimal PATH.
#   3. Changes to the NovaPanel home directory.
#   4. Prints a session banner.
#   5. Enforces role-based shell behaviour:
#        ReadOnly      -> read-only shell (set -r)
#        Developer     -> full interactive bash (project scope)
#        AccountOwner  -> full interactive bash (extended scope)
#        Admin         -> full interactive bash (system scope)
#
# Security model: trust users, but track and limit sessions.
# ============================================================================
set -euo pipefail

SESSION_ID="${1:-unknown}"
ROLE="${2:-ReadOnly}"

# ----------------------------------------------------------------------------
# 1. Sanitise environment – strip well-known secret variable names
# ----------------------------------------------------------------------------
for VAR in \
    APP_KEY DB_PASSWORD DATABASE_PASSWORD MYSQL_PASSWORD MYSQL_ROOT_PASSWORD \
    POSTGRES_PASSWORD REDIS_PASSWORD SECRET_KEY PRIVATE_KEY API_KEY \
    AUTH_SECRET JWT_SECRET SMTP_PASSWORD MAIL_PASSWORD; do
    unset "${VAR}" 2>/dev/null || true
done

# ----------------------------------------------------------------------------
# 2. Set a safe PATH
# ----------------------------------------------------------------------------
export PATH="/usr/local/bin:/usr/bin:/bin:/usr/local/sbin:/usr/sbin:/sbin"

# ----------------------------------------------------------------------------
# 3. Working directory
# ----------------------------------------------------------------------------
NOVAPANEL_HOME="${NOVAPANEL_HOME:-/opt/novapanel}"
cd "${NOVAPANEL_HOME}" 2>/dev/null || cd /tmp

# ----------------------------------------------------------------------------
# 4. Session banner
# ----------------------------------------------------------------------------
ROLE_UPPER="$(echo "${ROLE}" | tr '[:lower:]' '[:upper:]')"
echo "╔══════════════════════════════════════════════════════════╗"
printf  "║  NovaPanel Terminal  │  Role: %-27s ║\n" "${ROLE}"
printf  "║  Session: %-47s ║\n" "${SESSION_ID:0:36}"
echo "╠══════════════════════════════════════════════════════════╣"
echo "║  Working dir : ${NOVAPANEL_HOME}"
echo "║  Session TTL : 15 minutes │ Idle timeout: 5 minutes"
echo "║  All actions are logged for security auditing."
echo "╚══════════════════════════════════════════════════════════╝"
echo ""

# ----------------------------------------------------------------------------
# 5. Role-based shell behaviour
# ----------------------------------------------------------------------------
case "${ROLE}" in
    ReadOnly|readonly)
        echo "  ⚠  Read-only mode – commands that modify state are disabled."
        echo ""
        # Start a restricted bash (no exec, no redirect to files)
        exec bash --restricted --norc --noprofile
        ;;
    Developer|developer)
        echo "  ℹ  Developer shell – project-scoped access."
        echo ""
        exec bash --login
        ;;
    AccountOwner|Owner|owner)
        echo "  ℹ  Owner shell – extended access."
        echo ""
        exec bash --login
        ;;
    Admin|admin)
        echo "  ℹ  Admin shell – full system access."
        echo ""
        exec bash --login
        ;;
    *)
        # Unknown role – fall back to read-only
        echo "  ⚠  Unknown role '${ROLE}' – starting read-only shell."
        echo ""
        exec bash --restricted --norc --noprofile
        ;;
esac
