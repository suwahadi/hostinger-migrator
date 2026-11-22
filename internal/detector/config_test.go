package detector

import (
	"strings"
	"testing"
)

var newCred = Credential{Name: "u2_wp", User: "u2_wpuser", Password: "Baru-Pass_123", Host: "localhost"}

func TestUpdateWPConfig(t *testing.T) {
	out, err := UpdateCredentials("public_html/wp-config.php", wpConfig, newCred)
	if err != nil {
		t.Fatal(err)
	}
	ref := parseWPConfig(out)
	if ref.Name != "u2_wp" || ref.User != "u2_wpuser" || ref.Password != "Baru-Pass_123" || ref.Host != "localhost" {
		t.Fatalf("ref = %+v\n%s", ref, out)
	}
	if !strings.Contains(out, "$table_prefix = 'wp_';") || !strings.Contains(out, "komentar_lama") {
		t.Fatal("isi lain harus tetap")
	}
}

func TestUpdateEnvAndCI4(t *testing.T) {
	env := "APP_KEY=x\nDB_CONNECTION=mysql\nDB_HOST=127.0.0.1\nDB_DATABASE=lama\nDB_USERNAME=lama\nDB_PASSWORD=\"lama pass\"\nCACHE=file\n"
	out, err := UpdateCredentials(".env", env, newCred)
	if err != nil {
		t.Fatal(err)
	}
	e := ParseEnv(out)
	if e["DB_DATABASE"] != "u2_wp" || e["DB_PASSWORD"] != "Baru-Pass_123" || e["APP_KEY"] != "x" || e["CACHE"] != "file" {
		t.Fatalf("env = %v", e)
	}
	ci4 := "database.default.hostname = localhost\ndatabase.default.database = a\ndatabase.default.username = a\ndatabase.default.password = a\n"
	out, err = UpdateCredentials(".env", ci4, Credential{Name: "b", User: "b", Password: "p w#", Host: "h"})
	if err != nil {
		t.Fatal(err)
	}
	if ParseEnv(out)["database.default.password"] != "p w#" {
		t.Fatalf("ci4 = %s", out)
	}
}

func TestUpdateCI3AndJoomla(t *testing.T) {
	ci := "'hostname' => 'localhost',\n'username' => 'a',\n'password' => 'b',\n'database' => 'c',"
	out, err := UpdateCredentials("application/config/database.php", ci, newCred)
	if err != nil || parseCI3(out).Password != "Baru-Pass_123" || parseCI3(out).Name != "u2_wp" {
		t.Fatalf("ci3 = %s %v", out, err)
	}
	jm := "public $host = 'localhost';\npublic $user = 'a';\npublic $password = 'b';\npublic $db = 'c';"
	out, err = UpdateCredentials("configuration.php", jm, Credential{Name: "n", User: "u", Password: "it's", Host: "h"})
	if err != nil || !strings.Contains(out, `public $password = 'it\'s';`) || parseJoomla(out).Name != "n" {
		t.Fatalf("joomla = %s %v", out, err)
	}
}

func TestUpdateMissingKeyAndUnknown(t *testing.T) {
	if _, err := UpdateCredentials("wp-config.php", "define('DB_NAME','x');", newCred); err == nil {
		t.Fatal("kunci kurang harus error")
	}
	if _, err := UpdateCredentials("x.ini", "", newCred); err == nil {
		t.Fatal("format tidak dikenal harus error")
	}
	if ref := CredentialsFrom(".env", "DB_DATABASE=a\nDB_USERNAME=b\nDB_PASSWORD=c\n"); ref.Password != "c" {
		t.Fatalf("ref = %+v", ref)
	}
	if ref := CredentialsFrom("public_html/wp-config.php", wpConfig); ref.Password != "FAKE-wp-pass-123" {
		t.Fatalf("ref = %+v", ref)
	}
}
