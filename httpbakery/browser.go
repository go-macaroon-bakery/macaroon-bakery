package httpbakery

import (
	"fmt"
	"net/url"
	"os"

	"github.com/juju/webbrowser"
)

// OpenWebBrowser opens a web browser at the
// given URL. If the OS is not recognised, the URL
// is just printed to standard output.
func OpenWebBrowser(url *url.URL) error {
	err := webbrowser.Open(url)
	if err == nil {
		fmt.Fprintf(os.Stderr, "Opening an authorization web page in your browser.\n")
		fmt.Fprintf(os.Stderr, "If it does not open, please open this URL:\n%s\n", url)
		return nil
	}
	if err == webbrowser.ErrNoBrowser {
		fmt.Fprintf(os.Stderr, "Please open this URL in your browser to authorize:\n%s\n", url)
		return nil
	}
	return err
}
