package office

import "testing"

// A Document Server cannot download files from an address that only means
// "this machine" to the browser: such bases get a hint.
func TestIsLoopbackBase(t *testing.T) {
	for base, want := range map[string]bool{
		"http://127.0.0.1:8090":     true,
		"http://localhost:8080":     true,
		"http://[::1]:8080":         true,
		"http://192.168.50.21:2020": false,
		"https://cloud.example.com": false,
		"http://easycloud:8080":     false,
	} {
		if got := isLoopbackBase(base); got != want {
			t.Errorf("isLoopbackBase(%q) = %v, want %v", base, got, want)
		}
	}
}
