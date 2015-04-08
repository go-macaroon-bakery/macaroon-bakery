package httpbakery

import (
	"bytes"
	"fmt"
	"net/url"
	"os"
	"os/exec"
	"runtime"
	"strings"

	"gopkg.in/errgo.v1"
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
		cmd := exec.Command(args[0], args[1:]...)
		data, err := cmd.CombinedOutput()
		if err == nil {
			fmt.Fprintf(os.Stderr, "A page has been opened in your web browser. Please authorize there.\n")
			return nil
		}
		if err != exec.ErrNotFound {
			if _, ok := err.(*exec.ExitError); ok {
				return errgo.Newf("cannot open web browser: %s", bytes.TrimSpace(data))
			}
			return errgo.Notef(err, "cannot open web browser")
		}
	}
	fmt.Fprintf(os.Stderr, "Please visit this web page:\n%s\n", url)
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
