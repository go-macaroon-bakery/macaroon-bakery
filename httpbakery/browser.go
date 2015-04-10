package httpbakery

import (
	"fmt"
	"net/url"
	"os"
	"os/exec"
	"runtime"
	"strings"
)

var browser = map[string]string{
	"linux":   "sensible-browser",
	"darwin":  "open",
	"freebsd": "xdg-open",
	"netbsd":  "xdg-open",
	"openbsd": "xdg-open",
}

// OpenWebBrowser opens a web browser at the
// given URL. If the OS is not recognised, the URL
// is just printed to standard output.
func OpenWebBrowser(url *url.URL) error {
	var args []string
	if runtime.GOOS == "windows" {
		// Windows is special because the start command is
		// built into cmd.exe and hence requires the argument
		// to be quoted.
		args = []string{"cmd", "/c", "start", winCmdQuote.Replace(url.String())}
	} else if b := browser[runtime.GOOS]; b != "" {
		args = []string{b, url.String()}
	}
	if args != nil {
		fmt.Fprintf(os.Stderr, "Opening an authorization web page in your browser.\n")
		fmt.Fprintf(os.Stderr, "If it does not open, please open this URL:\n%s\n", url)
		cmd := exec.Command(args[0], args[1:]...)
		cmd.Stdout = os.Stdout
		cmd.Stderr = os.Stderr
		cmd.Start()
		go cmd.Wait()
	} else {
		fmt.Fprintf(os.Stderr, "Please open this URL in your browser to authorize:\n%s\n", url)
	}
	return nil
}

// winCmdQuote can quote metacharacters special to the Windows
// cmd.exe command interpreter. It does that by inserting
// a '^' character before each metacharacter. Note that
// most of these cannot actually be produced by URL.String,
// but we include them for completeness.
var winCmdQuote = strings.NewReplacer(
	"&", "^&",
	"%", "^%",
	"(", "^(",
	")", "^)",
	"^", "^^",
	"<", "^<",
	">", "^>",
	"|", "^|",
)
