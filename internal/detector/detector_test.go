package detector

import (
	"strings"
	"testing"
)

const wpConfig = `<?php
# define( 'DB_NAME', 'komentar_lama' );
define( 'DB_NAME', 'u494983133_wp' );
define( 'DB_USER', "u494983133_user" );
define( 'DB_PASSWORD', 'FAKE-wp-pass-123' );
define( 'DB_HOST', 'localhost' );
$table_prefix = 'wp_';
`

func TestDetectWordPress(t *testing.T) {
	r := Detect(Input{
		RootEntries:   []string{"public_html/", "logs/"},
		PublicEntries: []string{"wp-config.php", "wp-content/", "wp-includes/", "index.php", ".htaccess"},
		Files:         map[string]string{"public_html/wp-config.php": wpConfig},
	})
	if r.Type != TypeWordPress || r.Confidence < 0.9 {
		t.Fatalf("r = %+v", r)
	}
	if len(r.DBRefs) != 1 {
		t.Fatalf("db = %+v", r.DBRefs)
	}
	ref := r.DBRefs[0]
	if ref.Name != "u494983133_wp" || ref.User != "u494983133_user" || ref.Host != "localhost" || !ref.HasPassword || ref.Source != "public_html/wp-config.php" {
		t.Fatalf("ref = %+v", ref)
	}
}

func TestDetectWordPressWeakSignal(t *testing.T) {
	r := Detect(Input{PublicEntries: []string{"wp-config.php", "index.php"}})
	if r.Type != TypeWordPress || r.Confidence >= 0.9 {
		t.Fatalf("r = %+v", r)
	}
}

func TestDetectLaravelOutsidePublicHTML(t *testing.T) {
	r := Detect(Input{
		RootEntries:   []string{"app/", "bootstrap/", "public_html/", "vendor/", "artisan", "composer.json", ".env"},
		PublicEntries: []string{"index.php", ".htaccess"},
		Files: map[string]string{
			"composer.json": `{"require":{"laravel/framework":"^11.0"}}`,
			".env":          "APP_KEY=base64:FAKEkey\nDB_CONNECTION=mysql\nDB_HOST=127.0.0.1\nDB_DATABASE=\"u494983133_toko\"\nDB_USERNAME=u494983133_toko\nDB_PASSWORD='FAKE-env-pass'\n",
		},
	})
	if r.Type != TypeLaravel || r.Confidence < 0.9 {
		t.Fatalf("r = %+v", r)
	}
	if len(r.Notes) == 0 || !strings.Contains(r.Notes[0], "di luar public_html") {
		t.Fatalf("notes = %v", r.Notes)
	}
	if len(r.DBRefs) != 1 || r.DBRefs[0].Name != "u494983133_toko" || r.DBRefs[0].Host != "127.0.0.1" || !r.DBRefs[0].HasPassword {
		t.Fatalf("db = %+v", r.DBRefs)
	}
}

func TestDetectLaravelSqliteHasNoMySQLRef(t *testing.T) {
	r := Detect(Input{
		RootEntries: []string{"artisan", "composer.json", ".env"},
		Files:       map[string]string{"composer.json": `laravel/framework`, ".env": "DB_CONNECTION=sqlite\nDB_DATABASE=/x/db.sqlite\n"},
	})
	if r.Type != TypeLaravel || len(r.DBRefs) != 0 {
		t.Fatalf("r = %+v", r)
	}
}

func TestDetectCodeIgniter3And4(t *testing.T) {
	ci3 := Detect(Input{
		PublicEntries: []string{"system/", "application/", "index.php"},
		Files: map[string]string{"public_html/application/config/database.php": `$db['default'] = array(
	'hostname' => 'localhost',
	'username' => 'u1_ci',
	'password' => 'FAKE-ci-pass',
	'database' => 'u1_ci',
);`},
	})
	if ci3.Type != TypeCodeIgniter || len(ci3.DBRefs) != 1 || ci3.DBRefs[0].Name != "u1_ci" || !ci3.DBRefs[0].HasPassword {
		t.Fatalf("ci3 = %+v", ci3)
	}
	ci4 := Detect(Input{
		RootEntries: []string{"app/", "public_html/", "spark", ".env"},
		Files:       map[string]string{".env": "database.default.hostname = localhost\ndatabase.default.database = u1_ci4\ndatabase.default.username = u1_ci4\ndatabase.default.password = FAKE-ci4\n"},
	})
	if ci4.Type != TypeCodeIgniter || len(ci4.DBRefs) != 1 || ci4.DBRefs[0].Name != "u1_ci4" {
		t.Fatalf("ci4 = %+v", ci4)
	}
}

func TestDetectJoomla(t *testing.T) {
	r := Detect(Input{
		PublicEntries: []string{"configuration.php", "administrator/", "components/"},
		Files: map[string]string{"public_html/configuration.php": `class JConfig {
	public $host = 'localhost';
	public $user = 'u1_jm';
	public $password = 'FAKE-jm';
	public $db = 'u1_jm';
}`},
	})
	if r.Type != TypeJoomla || len(r.DBRefs) != 1 || r.DBRefs[0].User != "u1_jm" {
		t.Fatalf("r = %+v", r)
	}
}

func TestPackageJSONAloneIsNotNode(t *testing.T) {
	r := Detect(Input{
		PublicEntries: []string{"index.html", "assets/"},
		RootEntries:   []string{"package.json", "public_html/"},
		Files:         map[string]string{"package.json": `{"scripts":{"start":"vite","build":"vite build"}}`},
		APIType:       "other",
	})
	if r.Type == TypeNodeJS {
		t.Fatal("package.json saja tidak boleh dianggap Node.js")
	}
	if r.Type != TypeStatic {
		t.Fatalf("harus Statis, dapat %s", r.Type)
	}
	if len(r.Notes) == 0 || !strings.Contains(r.Notes[0], "build frontend") {
		t.Fatalf("notes = %v", r.Notes)
	}
}

func TestDetectNode(t *testing.T) {
	r := Detect(Input{
		RootEntries: []string{"package.json", "server.js", "public_html/"},
		Files:       map[string]string{"package.json": `{"scripts":{"start":"node server.js"}}`},
		APIType:     "nodejs",
	})
	if r.Type != TypeNodeJS || r.Confidence < 0.9 {
		t.Fatalf("r = %+v", r)
	}
	r = Detect(Input{APIType: "nodejs"})
	if r.Type != TypeNodeJS || r.Confidence >= 0.9 {
		t.Fatalf("hanya API = %+v", r)
	}
}

func TestDetectPHPStaticOther(t *testing.T) {
	php := Detect(Input{PublicEntries: []string{"index.php", "koneksi.php"}})
	if php.Type != TypePHP || len(php.Notes) == 0 {
		t.Fatalf("php = %+v", php)
	}
	st := Detect(Input{PublicEntries: []string{"index.html", "style.css"}})
	if st.Type != TypeStatic {
		t.Fatalf("statis = %+v", st)
	}
	other := Detect(Input{PublicEntries: []string{"default.php.bak", "data/"}, APIType: "wordpress"})
	if other.Type != TypeOther || len(other.Notes) != 2 {
		t.Fatalf("lainnya = %+v", other)
	}
}

func TestParseEnv(t *testing.T) {
	env := ParseEnv("# komentar\nA=1\nexport B=\"dua kata\"\nC='tiga' \nD=empat # catatan\r\nTANPA_SAMA\n")
	if env["A"] != "1" || env["B"] != "dua kata" || env["C"] != "tiga" || env["D"] != "empat" {
		t.Fatalf("env = %v", env)
	}
	if _, ok := env["TANPA_SAMA"]; ok {
		t.Fatal("baris tanpa = harus diabaikan")
	}
}

func TestExtractSecrets(t *testing.T) {
	secrets := ExtractSecrets(wpConfig + "\nAPP_KEY=base64:FAKEappkey123\nDB_PASSWORD=FAKE-env-pass\n'password' => 'FAKE-ci-pass',\npublic $password = 'FAKE-jm-pass';\n")
	joined := strings.Join(secrets, "|")
	for _, want := range []string{"FAKE-wp-pass-123", "base64:FAKEappkey123", "FAKE-env-pass", "FAKE-ci-pass", "FAKE-jm-pass"} {
		if !strings.Contains(joined, want) {
			t.Errorf("rahasia %q tidak terdeteksi: %v", want, secrets)
		}
	}
}
