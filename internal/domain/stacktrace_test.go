package domain

import (
	"strings"
	"testing"
)

func TestFilterVendorStackTrace_UserExample(t *testing.T) {
	input := `#0 /var/www/html/vendor/laravel/framework/src/Illuminate/Database/Connection.php(712): Illuminate\Database\Connection->runQueryCallback()
#1 /var/www/html/app/Http/Controllers/UserController.php(42): App\Models\User::all()
#2 /var/www/html/public/index.php(52): App\Http\Kernel->handle()`

	expected := `#1 /var/www/html/app/Http/Controllers/UserController.php(42): App\Models\User::all()
#2 /var/www/html/public/index.php(52): App\Http\Kernel->handle()`

	got := FilterVendorStackTrace(input)
	if strings.TrimSpace(got) != strings.TrimSpace(expected) {
		t.Fatalf("expected:\n%s\ngot:\n%s", expected, got)
	}

	stats := GetStackTraceStats(input)
	if stats.TotalFrames != 3 {
		t.Errorf("expected 3 total frames, got %d", stats.TotalFrames)
	}
	if stats.AppFrames != 2 {
		t.Errorf("expected 2 app frames, got %d", stats.AppFrames)
	}
}

func TestFilterVendorStackTrace_WithMain(t *testing.T) {
	input := `#0 /var/www/html/vendor/laravel/framework/Routing/Router.php(10): run()
#1 /var/www/html/app/Http/Controllers/OrderController.php(25): find()
#2 {main}`

	expected := `#1 /var/www/html/app/Http/Controllers/OrderController.php(25): find()`

	got := FilterVendorStackTrace(input)
	if strings.TrimSpace(got) != strings.TrimSpace(expected) {
		t.Fatalf("expected:\n%s\ngot:\n%s", expected, got)
	}

	stats := GetStackTraceStats(input)
	if stats.TotalFrames != 2 { // {main} is ignored in frame count
		t.Errorf("expected 2 total frames, got %d", stats.TotalFrames)
	}
	if stats.AppFrames != 1 {
		t.Errorf("expected 1 app frame, got %d", stats.AppFrames)
	}
}

func TestFilterVendorStackTrace_WindowsPaths(t *testing.T) {
	input := `#0 C:\project\vendor\laravel\framework\src\Connection.php(712): runQuery()
#1 C:\project\app\Http\Controllers\UserController.php(42): all()`

	expected := `#1 C:\project\app\Http\Controllers\UserController.php(42): all()`

	got := FilterVendorStackTrace(input)
	if strings.TrimSpace(got) != strings.TrimSpace(expected) {
		t.Fatalf("expected:\n%s\ngot:\n%s", expected, got)
	}
}

func TestFilterVendorStackTrace_VendorInAppName(t *testing.T) {
	input := `#0 /var/www/html/vendor/symfony/routing/Router.php(50): match()
#1 /var/www/html/app/Http/Controllers/VendorController.php(30): listVendors()
#2 /var/www/html/app/Models/Vendor.php(15): get()`

	expected := `#1 /var/www/html/app/Http/Controllers/VendorController.php(30): listVendors()
#2 /var/www/html/app/Models/Vendor.php(15): get()`

	got := FilterVendorStackTrace(input)
	if strings.TrimSpace(got) != strings.TrimSpace(expected) {
		t.Fatalf("expected:\n%s\ngot:\n%s", expected, got)
	}
}

func TestFilterVendorStackTrace_AllVendor(t *testing.T) {
	input := `#0 /var/www/html/vendor/laravel/framework/Routing/Router.php(10): run()
#1 /var/www/html/vendor/composer/ClassLoader.php(20): load()
#2 {main}`

	got := FilterVendorStackTrace(input)
	if strings.TrimSpace(got) != "" {
		t.Fatalf("expected empty string, got:\n%s", got)
	}

	stats := GetStackTraceStats(input)
	if stats.TotalFrames != 2 {
		t.Errorf("expected 2 total frames, got %d", stats.TotalFrames)
	}
	if stats.AppFrames != 0 {
		t.Errorf("expected 0 app frames, got %d", stats.AppFrames)
	}
}

func TestIncident_Methods(t *testing.T) {
	inc := Incident{
		StackTrace: `#0 /var/www/html/vendor/framework.php(10): run()
#1 /var/www/html/app/Controller.php(20): exec()`,
	}

	appTrace := inc.AppStackTrace()
	expected := `#1 /var/www/html/app/Controller.php(20): exec()`
	if strings.TrimSpace(appTrace) != strings.TrimSpace(expected) {
		t.Fatalf("expected:\n%s\ngot:\n%s", expected, appTrace)
	}

	stats := inc.StackTraceStats()
	if stats.TotalFrames != 2 || stats.AppFrames != 1 {
		t.Fatalf("unexpected stats: %+v", stats)
	}
}
