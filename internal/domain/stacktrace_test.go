package domain

import (
	"fmt"
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

func TestParseFramesGroupsAndCallGraph(t *testing.T) {
	raw := `#0 /var/www/html/app/Services/PaymentService.php(87): App\Services\PaymentGateway->charge(Object(App\Models\Order))
#1 /var/www/html/vendor/laravel/framework/src/Illuminate/Database/Connection.php(790): Illuminate\Database\Connection->runQueryCallback('select', Array)
#2 /var/www/html/vendor/laravel/framework/src/Illuminate/Database/Connection.php(791): Illuminate\Database\Connection->select('select')
#3 [internal function]: App\Http\Controllers\CheckoutController->store(Object(Illuminate\Http\Request))
#4 /var/www/html/app/Http/Controllers/CheckoutController.php(41): App\Http\Controllers\CheckoutController::boot()
#5 /var/www/html/vendor/symfony/http-kernel/HttpKernel.php(12): handle()
#6 {main}`

	frames := ParseFrames(raw)
	if len(frames) != 6 {
		t.Fatalf("expected 6 frames without {main}, got %d", len(frames))
	}
	first := frames[0]
	if first.File != "/var/www/html/app/Services/PaymentService.php" || first.Line != 87 ||
		first.Class != `App\Services\PaymentGateway` || first.Sep != "->" || first.Method != "charge" || first.Vendor {
		t.Errorf("unexpected first frame: %+v", first)
	}
	if first.ShortClass() != "PaymentGateway" || first.FileName() != "PaymentService.php" {
		t.Errorf("unexpected short names: %q %q", first.ShortClass(), first.FileName())
	}
	if f := frames[3]; f.File != "[internal function]" || f.Line != 0 || f.Method != "store" {
		t.Errorf("unexpected internal frame: %+v", f)
	}
	if f := frames[5]; !f.Vendor || f.Class != "" || f.Method != "handle" {
		t.Errorf("unexpected function frame: %+v", f)
	}

	var shape []int
	for _, g := range GroupFrames(frames) {
		n := len(g.Frames)
		if g.Vendor {
			n = -n
		}
		shape = append(shape, n)
	}
	if got, want := fmt.Sprint(shape), "[1 -2 1 1 -1]"; got != want {
		t.Errorf("groups: got %s, want %s (negative = vendor run)", got, want)
	}

	graph := BuildCallGraph(frames)
	if len(graph) != 4 {
		t.Fatalf("expected 4 graph nodes, got %d: %+v", len(graph), graph)
	}
	if !graph[0].Group || graph[0].Packages[0] != "symfony/http-kernel" {
		t.Errorf("entry node should be the vendor group: %+v", graph[0])
	}
	if graph[1].Class != `App\Http\Controllers\CheckoutController` || fmt.Sprint(graph[1].Calls) != "[boot:41 store]" {
		t.Errorf("same-class calls should merge: %+v", graph[1])
	}
	if !graph[2].Group || graph[2].Count != 2 || fmt.Sprint(graph[2].Packages) != "[laravel/framework]" {
		t.Errorf("vendor run should collapse: %+v", graph[2])
	}
	if graph[3].Class != `App\Services\PaymentGateway` || graph[3].Vendor {
		t.Errorf("last node should be the throwing app frame: %+v", graph[3])
	}
}
