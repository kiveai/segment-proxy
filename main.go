package main

import (
	"flag"
	"fmt"
	"log"
	"net/http"
	"net/http/httputil"
	"net/url"
	"os"
	"strings"

	"github.com/gorilla/handlers"
)

// singleJoiningSlash is copied from httputil.singleJoiningSlash method.
func singleJoiningSlash(a, b string) string {
	aslash := strings.HasSuffix(a, "/")
	bslash := strings.HasPrefix(b, "/")
	switch {
	case aslash && bslash:
		return a + b[1:]
	case !aslash && !bslash:
		return a + "/" + b
	}
	return a + b
}

func isBot(req *http.Request, bots []string) bool {

	for _, ua := range bots {
        if strings.Contains(req.UserAgent(), ua) {
            return true
        }
    }
    return false
}

var bots []string= []string{ "kive-bot-tester",
	"googlebot",
	"yahoo! slurp",
	"bingbot",
	"yandex",
	"baiduspider",
	"facebookexternalhit",
	"twitterbot",
	"rogerbot",
	"linkedinbot",
	"embedly",
	"quora link preview",
	"showyoubot",
	"outbrain",
	"pinterest/0.",
	"developers.google.com/+/web/snippet",
	"slackbot",
	"vkshare",
	"w3c_validator",
	"redditbot",
	"applebot",
	"whatsapp",
	"flipboard",
	"tumblr",
	"bitlybot",
	"skypeuripreview",
	"nuzzel",
	"discordbot",
	"google page speed",
	"qwantify",
	"pinterestbot",
	"bitrix link preview",
	"xing-contenttabreceiver",
	"chrome-lighthouse",
	"telegrambot",
	"ahrefsbot",
	"ahrefssiteaudit",
	"Prerender"}


// NewSegmentReverseProxy is adapted from the httputil.NewSingleHostReverseProxy
// method, modified to dynamically redirect to different servers (CDN or Tracking API)
// based on the incoming request, and sets the host of the request to the host of of
// the destination URL.
func NewSegmentReverseProxy(cdn *url.URL, trackingAPI *url.URL) http.Handler {
	director := func(req *http.Request) {

		// Figure out which server to redirect to based on the incoming request.
		var target *url.URL
		if strings.HasPrefix(req.URL.String(), "/v1/projects") || strings.HasPrefix(req.URL.String(), "/analytics.js/v1") || strings.HasPrefix(req.URL.String(), "/analytics-next/bundles") || strings.HasPrefix(req.URL.String(), "/next-integrations/") {
			target = cdn
		} else {
			
			
			target = trackingAPI
		}

		targetQuery := target.RawQuery
		req.URL.Scheme = target.Scheme
		req.URL.Host = target.Host
		req.URL.Path = singleJoiningSlash(target.Path, req.URL.Path)
		if targetQuery == "" || req.URL.RawQuery == "" {
			req.URL.RawQuery = targetQuery + req.URL.RawQuery
		} else {
			req.URL.RawQuery = targetQuery + "&" + req.URL.RawQuery
		}

		// Set the host of the request to the host of of the destination URL.
		// See http://blog.semanticart.com/blog/2013/11/11/a-proper-api-proxy-written-in-go/.
		req.Host = req.URL.Host
	}
	return &httputil.ReverseProxy{Director: director}
}

var port = flag.String("port", "8080", "bind address")
var debug = flag.Bool("debug", false, "debug mode")

func main() {
	flag.Parse()
	cdnURL, err := url.Parse("https://cdn.segment.com")
	if err != nil {
		log.Fatal(err)
	}
	trackingAPIURL, err := url.Parse("https://api.segment.io")
	if err != nil {
		log.Fatal(err)
	}
	proxy := NewSegmentReverseProxy(cdnURL, trackingAPIURL)
	if *debug {
		proxy = handlers.LoggingHandler(os.Stdout, proxy)
		log.Printf("serving proxy at port %v\n", *port)
	}

	http.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {

        if isBot(r, bots) {
            // pass the request to the reverse proxy
			var userAgent= r.UserAgent()
			requestDump, err := httputil.DumpRequest(r, true)
			if err != nil {
  				fmt.Println(err)
			}
			message := fmt.Sprintf("Ignored request with userAgent %s and full request %s", userAgent, string(requestDump))
			log.Println(message)
			w.WriteHeader(http.StatusOK)
			return
            
        } else {
			proxy.ServeHTTP(w, r)
        }
    })
	log.Fatal(http.ListenAndServe(":"+*port, nil))
}
