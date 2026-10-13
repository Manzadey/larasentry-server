package github

import (
	"strings"
	"testing"
)

func TestExtractSnippet(t *testing.T) {
	sampleFile := strings.TrimSpace(`
<?php

namespace App\Http\Controllers;

use App\Services\OrderService;
use Illuminate\Http\Request;

class OrderController extends Controller
{
    public function checkout(Request $request, OrderService $service)
    {
        $cart = $request->user()->cart();
        $order = $service->createFromCart($cart);
        $total = $cart->items->sum('price');
        return response()->json(['order_id' => $order->id, 'total' => $total]);
    }
}
`)

	t.Run("Standard extraction with radius 3", func(t *testing.T) {
		// Строка 14 — та, где ошибка: "$total = $cart->items->sum('price');"
		// Радиус 3: строки с 11 по 17
		lines := ExtractSnippet(sampleFile, 14, 3)
		if len(lines) != 7 {
			t.Fatalf("expected 7 lines, got %d", len(lines))
		}

		if lines[0].Number != 11 {
			t.Errorf("expected first line number 11, got %d", lines[0].Number)
		}
		if lines[len(lines)-1].Number != 17 {
			t.Errorf("expected last line number 17, got %d", lines[len(lines)-1].Number)
		}

		var foundError bool
		for _, l := range lines {
			if l.Number == 14 {
				if !l.IsError {
					t.Errorf("expected line 14 to have IsError=true")
				}
				if !strings.Contains(l.Code, "sum('price')") {
					t.Errorf("unexpected line 14 code content: %s", l.Code)
				}
				foundError = true
			} else {
				if l.IsError {
					t.Errorf("expected line %d to have IsError=false", l.Number)
				}
			}
		}
		if !foundError {
			t.Errorf("line 14 not found in extracted lines")
		}
	})

	t.Run("Target line near start of file (clamps at 1)", func(t *testing.T) {
		lines := ExtractSnippet(sampleFile, 2, 5)
		if len(lines) == 0 {
			t.Fatalf("expected non-empty lines")
		}
		if lines[0].Number != 1 {
			t.Errorf("expected start at line 1, got %d", lines[0].Number)
		}
		if lines[1].Number != 2 || !lines[1].IsError {
			t.Errorf("expected line 2 to be error line")
		}
	})

	t.Run("Target line near end of file", func(t *testing.T) {
		lines := ExtractSnippet(sampleFile, 17, 5)
		if len(lines) == 0 {
			t.Fatalf("expected non-empty lines")
		}
		if lines[len(lines)-1].Number != 17 {
			t.Errorf("expected end at line 17, got %d", lines[len(lines)-1].Number)
		}
	})

	t.Run("Empty file or invalid target line", func(t *testing.T) {
		lines := ExtractSnippet("", 5, 3)
		if len(lines) != 0 {
			t.Errorf("expected 0 lines for empty file, got %d", len(lines))
		}

		lines2 := ExtractSnippet(sampleFile, 0, 3)
		if len(lines2) != 0 {
			t.Errorf("expected 0 lines for line 0, got %d", len(lines2))
		}
	})
}
