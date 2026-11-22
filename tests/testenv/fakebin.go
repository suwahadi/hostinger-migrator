package testenv

import (
	"os"
	"path/filepath"
)

const fakeMysqldump = `#!/bin/bash
db="${@: -1}"
for a in "$@"; do
  if [ "$a" = "--events" ] && [ -f "$FAKEDB/noevents" ]; then
    echo "mysqldump: Couldn't execute 'SHOW EVENTS': Access denied for EVENT" >&2; exit 2
  fi
done
if [ ! -f "$FAKEDB/$db.sql" ]; then echo "mysqldump: Got error: 1049: Unknown database '$db'" >&2; exit 2; fi
if [ -f "$FAKEDB/$db.pwd" ] && [ "$(cat "$FAKEDB/$db.pwd")" != "$MYSQL_PWD" ]; then echo "mysqldump: Got error: 1045: Access denied for user" >&2; exit 2; fi
grep -v '^-- Dump completed' "$FAKEDB/$db.sql"
echo "-- Dump completed on 2026-11-22 10:00:00"
`

const fakeMysql = `#!/bin/bash
db=""; query=""
while [ $# -gt 0 ]; do
  case "$1" in
    -e) query="$2"; shift 2;;
    -h|-u) shift 2;;
    -N|-B) shift;;
    *) db="$1"; shift;;
  esac
done
if [ -f "$FAKEDB/$db.pwd" ] && [ "$(cat "$FAKEDB/$db.pwd")" != "$MYSQL_PWD" ]; then echo "ERROR 1045 (28000): Access denied for user" >&2; exit 1; fi
if [ ! -f "$FAKEDB/$db.pwd" ]; then echo "ERROR 1049 (42000): Unknown database '$db'" >&2; exit 1; fi
if [ -n "$query" ]; then
  case "$query" in *"SELECT 1"*) echo 1;; esac
  exit 0
fi
if [ -f "$FAKEDB/failimport" ]; then rm -f "$FAKEDB/failimport"; head -c 10 >/dev/null; echo "ERROR 2013 (HY000): Lost connection to MySQL server during query" >&2; exit 1; fi
cat > "$FAKEDB/$db.sql.tmp"
if grep -q "CREATE TABLE" "$FAKEDB/$db.sql.tmp"; then mv "$FAKEDB/$db.sql.tmp" "$FAKEDB/$db.sql"; else rm -f "$FAKEDB/$db.sql.tmp"; : > "$FAKEDB/$db.sql"; fi
`

const fakeWP = `#!/bin/bash
if [ "$1" = "search-replace" ] && [ -f .wp-siteurl ]; then
  from="$2"; to="$3"
  cur="$(cat .wp-siteurl)"
  if [ "$cur" = "$from" ]; then printf '%s' "$to" > .wp-siteurl; fi
  exit 0
fi
exit 0
`

func writeFakeBin(dir string) error {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	for name, body := range map[string]string{"mysqldump": fakeMysqldump, "mysql": fakeMysql, "wp": fakeWP} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o755); err != nil {
			return err
		}
	}
	return nil
}
