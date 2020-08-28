//package gae
package main

import (
	"bufio"
	"bytes"
	"compress/flate"
	"compress/gzip"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"io/ioutil"
	"mime"
	"net/http"
	"net/url"
	"path/filepath"
	"reflect"
	"regexp"
	"runtime"
	"strconv"
	"strings"
	"time"

	"net"
	"crypto/tls"
	"log"
	"os"
	"context"

	//OldApp "appengine"
	//OldUrl "appengine/urlfetch"
	//"google.golang.org/appengine"
	//alog "google.golang.org/appengine/log"
	//"google.golang.org/appengine/urlfetch"
	//"appengine"
	//"appengine/urlfetch"
)

const (
	Version  = "r9999"
	Password = ""

	DefaultFetchMaxSize        = 1024 * 1024 * 4
	DefaultDeadline            = 20 * time.Second
	DefaultOverquotaDelay      = 4 * time.Second
	DefaultURLFetchClosedDelay = 1 * time.Second
	DefaultDebug               = 0
)

func IsBinary(b []byte) bool {
	if len(b) > 3 && b[0] == 0xef && b[1] == 0xbb && b[2] == 0xbf {
		// utf-8 text
		return false
	}
	for i, c := range b {
		if c > 0x7f {
			return true
		}
		if c == '\n' && i > 4 {
			break
		}
		if i > 32 {
			break
		}
	}
	return false
}

func IsTextContentType(contentType string) bool {
	// text/* for html, plain text
	// application/{json, javascript} for ajax
	// application/{xml, rss, atom} for xml
	// application/x-www-form-urlencoded for some video api
	parts := strings.SplitN(contentType, "/", 2)
	mct := parts[0]
	if mct == "text" {
		return true
	}
	if mct == "application" {
		sct := strings.SplitN(parts[1], ";", 2)[0]
		return sct == "json" ||
			strings.HasSuffix(sct, "script") ||
			strings.HasSuffix(sct, "xml") ||
			strings.HasPrefix(sct, "xml") ||
			strings.HasPrefix(sct, "rss") ||
			strings.HasPrefix(sct, "atom") ||
			sct == "x-www-form-urlencoded"
	}
	return false
}

func ReadRequest(r io.Reader) (req *http.Request, err error) {
	req = new(http.Request)

	scanner := bufio.NewScanner(r)
	if scanner.Scan() {
		line := scanner.Text()
		parts := strings.Split(line, " ")
		if len(parts) != 3 {
			err = fmt.Errorf("Invaild Request Line: %#v", line)
			return
		}

		req.Method = parts[0]
		req.RequestURI = parts[1]

		if req.URL, err = url.Parse(req.RequestURI); err != nil {
			return
		}
		req.Host = req.URL.Host

		req.Header = http.Header{}
	}

	for scanner.Scan() {
		line := scanner.Text()
		parts := strings.SplitN(line, ":", 2)
		if len(parts) != 2 {
			continue
		}

		key := strings.TrimSpace(parts[0])
		value := strings.TrimSpace(parts[1])
		req.Header.Add(key, value)
	}

	if err = scanner.Err(); err != nil {
		// ignore
	}

	if cl := req.Header.Get("Content-Length"); cl != "" {
		if req.ContentLength, err = strconv.ParseInt(cl, 10, 64); err != nil {
			return
		}
	}

	req.Host = req.URL.Host
	if req.Host == "" {
		req.Host = req.Header.Get("Host")
	}

	return
}

/*func fmtError(c appengine.Context, err error) string {
	return fmt.Sprintf(`{
    "type": "appengine(%s, %s/%s)",
    "host": "%s",
    "software": "%s",
    "error": "%s"
}
`, runtime.Version(), runtime.GOOS, runtime.GOARCH, appengine.DefaultVersionHostname(c), appengine.ServerSoftware(), err.Error())
}*/
func fmtError(c context.Context, err error) string {
	//pjid := os.Getenv("GCP_PROJECT")
	//pjid := os.Getenv("GAE_APPLICATION")
	pjid := os.Getenv("GOOGLE_CLOUD_PROJECT")
	return fmt.Sprintf(`{
    "type": "appengine(%s, %s/%s)",
    "host": "%s",
    "software": "%s",
    "error": "%s"
}
`, runtime.Version(), runtime.GOOS, runtime.GOARCH, pjid+".appspot.com", os.Getenv("GAE_ENV"), err.Error())
}

func handlerError(c context.Context, rw http.ResponseWriter, err error, code int) {
	var b bytes.Buffer
	w, _ := flate.NewWriter(&b, flate.BestCompression)

	data := fmtError(c, err)
	fmt.Fprintf(w, "HTTP/1.1 %d\r\n", code)
	fmt.Fprintf(w, "Content-Type: text/plain; charset=utf-8\r\n")
	fmt.Fprintf(w, "Content-Length: %d\r\n", len(data))
	io.WriteString(w, "\r\n")
	io.WriteString(w, data)
	w.Close()

	b0 := []byte{0, 0}
	binary.BigEndian.PutUint16(b0, uint16(b.Len()))

	rw.Header().Set("Content-Type", "image/gif")
	//rw.Header().Set("Content-Length", strconv.Itoa(len(b0)+b.Len()))
	//rw.WriteHeader(http.StatusOK)
	rw.Write(b0)
	rw.Write(b.Bytes())
}

func handler(rw http.ResponseWriter, r *http.Request) {
	var err error
	//c := appengine.NewContext(r)
	c := r.Context()

	var hdrLen uint16
	if err := binary.Read(r.Body, binary.BigEndian, &hdrLen); err != nil {
		//c.Criticalf("binary.Read(&hdrLen) return %v", err)
		//log.Printf("binary.Read(&hdrLen) return %v", err)
		fmt.Fprintf(os.Stderr,"binary.Read(&hdrLen) return %v", err)
		handlerError(c, rw, err, http.StatusBadRequest)
		return
	}

	req, err := ReadRequest(bufio.NewReader(flate.NewReader(&io.LimitedReader{R: r.Body, N: int64(hdrLen)})))
	if err != nil {
		//c.Criticalf("http.ReadRequest(%#v) return %#v", r.Body, err)
		//log.Printf("http.ReadRequest(%#v) return %#v", r.Body, err)
		fmt.Fprintf(os.Stderr,"http.ReadRequest(%#v) return %#v", r.Body, err)
		handlerError(c, rw, err, http.StatusBadRequest)
		return
	}

	req.RemoteAddr = r.RemoteAddr
	req.TLS = r.TLS
	req.Body = r.Body
	defer req.Body.Close()

	params := make(map[string]string)
	if options := req.Header.Get("X-UrlFetch-Options"); options != "" {
		for _, pair := range strings.Split(options, ",") {
			parts := strings.SplitN(pair, "=", 2)
			if len(parts) == 1 {
				params[strings.TrimSpace(pair)] = ""
			} else {
				params[strings.TrimSpace(parts[0])] = strings.TrimSpace(parts[1])
			}
		}
		req.Header.Del("X-UrlFetch-Options")
	}

	oAE := req.Header.Get("Accept-Encoding")

	debug := DefaultDebug
	if s, ok := params["debug"]; ok && s != "" {
		if n, err := strconv.Atoi(s); err == nil {
			debug = n
		}
	}

	if debug > 1 {
		//c.Infof("Parsed Request=%#v\n", req)
		//log.Printf("Parsed Request=%#v\n", req)
		fmt.Fprintf(os.Stderr,"Parsed Request=%#v\n", req)
	}

	if Password != "" {
		password, ok := params["password"]
		if !ok {
			handlerError(c, rw, fmt.Errorf("urlfetch password required"), http.StatusForbidden)
			return
		} else if password != Password {
			handlerError(c, rw, fmt.Errorf("urlfetch password is wrong"), http.StatusForbidden)
			return
		}
	}

	deadline := DefaultDeadline
	if s, ok := params["deadline"]; ok && s != "" {
		if n, err := strconv.Atoi(s); err == nil {
			deadline = time.Duration(n) * time.Second
		}
	}

	fetchMaxSize := DefaultFetchMaxSize
	if s, ok := params["maxsize"]; ok && s != "" {
		if n, err := strconv.Atoi(s); err == nil {
			fetchMaxSize = n
		}
	}

	_, sslVerify := params["sslverify"]

	var resp *http.Response
         /*tlsdial := &tls.Dialer{
		         NetDialer: netdial,
			 Config:    tlsconf,
		    }*/
	//xzero := map[string]func(string,*tls.Conn) http.RoundTripper{}
	req = req.Clone(c)
	for i := 0; i < 2; i++ {
	   netdial := &net.Dialer{
                     Timeout:   30 * time.Second,
                     KeepAlive: 30 * time.Second,
                     DualStack: true,
                   }
           tlsconf := &tls.Config{
		          InsecureSkipVerify: !sslVerify,
		          //InsecureSkipVerify: true,
		          /*VerifyConnection: func(cs tls.ConnectionState) error {
			      opts := x509.VerifyOptions{
				DNSName:       cs.ServerName,
				Intermediates: x509.NewCertPool(),
			      }
			      for _, cert := range cs.PeerCertificates[1:] {
			        opts.Intermediates.AddCert(cert)
			      }
			      _, err := cs.PeerCertificates[0].Verify(opts)
			      return err
		          },*/
	           }
		/*t := &urlfetch.Transport{
			Context:                       c,
			Deadline:                      deadline,
			AllowInvalidServerCertificate: !sslVerify,
		}*/
                t := http.Transport{
                       DialContext: netdial.DialContext,
		       TLSClientConfig: tlsconf,
		       //DialTLSContext:  tlsdial.DialContext,
                       //ForceAttemptHTTP2:     false,
		       ResponseHeaderTimeout:        deadline,
                       //MaxIdleConns:          10,
                       //IdleConnTimeout:       30 * time.Second,
                       TLSHandshakeTimeout:   10 * time.Second,
                       ExpectContinueTimeout: 3 * time.Second,
		       DisableCompression:    true,
		       //TLSNextProto:          xzero,
	        }

		resp, err = t.RoundTrip(req)
		//resp, err = t.RoundTrip(req.Clone(c))
		if resp != nil && resp.Body != nil {
			if v := reflect.ValueOf(resp.Body).Elem().FieldByName("truncated"); v.IsValid() {
				if truncated := v.Bool(); truncated {
					resp.Body.Close()
					err = errors.New("URLFetchServiceError_RESPONSE_TOO_LARGE")
				}
			}
		}

		if err == nil {
			defer resp.Body.Close()
			break
		}

		message := err.Error()
		if strings.Contains(message, "RESPONSE_TOO_LARGE") {
			//c.Warningf("URLFetchServiceError %T(%v) deadline=%v, url=%v", err, err, deadline, req.URL.String())
			//log.Printf("URLFetchServiceError %T(%v) deadline=%v, url=%v", err, err, deadline, req.URL.String())
			fmt.Fprintf(os.Stderr,"URLFetchServiceError %T(%v) deadline=%v, url=%v", err, err, deadline, req.URL.String())
			if s := req.Header.Get("Range"); s != "" {
				if parts1 := strings.Split(s, "="); len(parts1) == 2 {
					if parts2 := strings.Split(parts1[1], "-"); len(parts2) == 2 {
						if start, err1 := strconv.Atoi(parts2[0]); err1 == nil {
							end, err1 := strconv.Atoi(parts2[1])
							if err1 != nil {
								req.Header.Set("Range", fmt.Sprintf("bytes=%d-%d", start, start+fetchMaxSize))
							} else {
								if end-start > fetchMaxSize {
									req.Header.Set("Range", fmt.Sprintf("bytes=%d-%d", start, start+fetchMaxSize))
								} else {
									req.Header.Set("Range", fmt.Sprintf("bytes=%d-%d", start, end))
								}
							}
						}
					}
				}
			} else {
				req.Header.Set("Range", fmt.Sprintf("bytes=0-%d", fetchMaxSize))
			}
		} else if strings.Contains(message, "Over quota") {
			//c.Warningf("URLFetchServiceError %T(%v) deadline=%v, url=%v", err, err, deadline, req.URL.String())
			//log.Printf("URLFetchServiceError %T(%v) deadline=%v, url=%v", err, err, deadline, req.URL.String())
			fmt.Fprintf(os.Stderr,"URLFetchServiceError %T(%v) deadline=%v, url=%v", err, err, deadline, req.URL.String())
			time.Sleep(DefaultOverquotaDelay)
		} else if strings.Contains(message, "urlfetch: CLOSED") {
			//c.Warningf("URLFetchServiceError %T(%v) deadline=%v, url=%v", err, err, deadline, req.URL.String())
			fmt.Fprintf(os.Stderr,"URLFetchServiceError %T(%v) deadline=%v, url=%v", err, err, deadline, req.URL.String())
			time.Sleep(DefaultURLFetchClosedDelay)
		} else {
			//c.Errorf("URLFetchServiceError %T(%v) deadline=%v, url=%v", err, err, deadline, req.URL.String())
			//log.Printf("URLFetchServiceError %T(%v) deadline=%v, url=%v", err, err, deadline, req.URL.String())
			fmt.Fprintf(os.Stderr,"URLFetchServiceError %T(%v) deadline=%v, url=%v", err, err, deadline, req.URL.String())
			break
		}
	}

	if err != nil {
		handlerError(c, rw, err, http.StatusBadGateway)
		return
	}

	// rewise resp.Header
	resp.Header.Del("Transfer-Encoding")
	if strings.ToLower(resp.Header.Get("Vary")) == "accept-encoding" {
		resp.Header.Del("Vary")
	}
	if resp.ContentLength > 0 {
		resp.Header.Set("Content-Length", strconv.FormatInt(resp.ContentLength, 10))
	}

	oCE := resp.Header.Get("Content-Encoding")
	chunked := false

	// urlfetch will try to decompress the content when "Accept-Encoding" does not contain "gzip"
	// delete "Content-Encoding" when content has decompressed with supported encoding
	if !strings.Contains(oAE, "gzip") &&
		(oCE == "gzip" || oCE == "deflate" || oCE == "br") {
		resp.Header.Del("Content-Encoding")
		oCE = ""
	}

	if oCE == "" &&
		IsTextContentType(resp.Header.Get("Content-Type")) {
		content := reflect.ValueOf(resp.Body).Elem().FieldByName("content").Bytes()
		switch {
		case strings.Contains(oAE, "gzip") && IsBinary(content):
			// urlfetch will remove "Content-Encoding: deflate" when "Accept-Encoding" contains "gzip"
			ext := filepath.Ext(req.URL.Path)
			if ext == "" || IsTextContentType(mime.TypeByExtension(ext)) {
				// ignore wrong "Content-Type"
				resp.Header.Set("Content-Encoding", "deflate")
			}
		case len(content) > 512:
			// we got plain text here, try compress it
			var bb bytes.Buffer
			var w io.WriteCloser
			var ce string

			switch {
			case r.Header.Get("User-Agent") == "Mozilla/5.0":
				// let App Engine automatically compress it
				chunked = true
			case strings.Contains(oAE, "deflate"):
				w, err = flate.NewWriter(&bb, flate.BestCompression)
				ce = "deflate"
			case strings.Contains(oAE, "gzip"):
				w, err = gzip.NewWriterLevel(&bb, gzip.BestCompression)
				ce = "gzip"
			}

			if err != nil {
				handlerError(c, rw, err, http.StatusBadGateway)
				return
			}

			if w != nil {
				w.Write(content)
				w.Close()

				bbLen := int64(bb.Len())
				if bbLen < resp.ContentLength {
					resp.Body = ioutil.NopCloser(&bb)
					resp.ContentLength = bbLen
					resp.Header.Set("Content-Length", strconv.FormatInt(resp.ContentLength, 10))
					resp.Header.Set("Content-Encoding", ce)
				}
			}
		}
	}

	if debug > 1 {
		//c.Infof("Write Response=%#v, chunked=%#v\n", resp, chunked)
		//log.Printf("Write Response=%#v, chunked=%#v\n", resp, chunked)
		fmt.Fprintf(os.Stderr,"Write Response=%#v, chunked=%#v\n", resp, chunked)
	}

	if debug > 0 {
		//c.Infof("%s \"%s %s %s\" %d %s", resp.Request.RemoteAddr, resp.Request.Method, resp.Request.URL.String(), resp.Request.Proto, resp.StatusCode, resp.Header.Get("Content-Length"))
		//log.Printf("%s \"%s %s %s\" %d %s", resp.Request.RemoteAddr, resp.Request.Method, resp.Request.URL.String(), resp.Request.Proto, resp.StatusCode, resp.Header.Get("Content-Length"))
		fmt.Fprintf(os.Stderr,"%s \"%s %s %s\" %d %s", resp.Request.RemoteAddr, resp.Request.Method, resp.Request.URL.String(), resp.Request.Proto, resp.StatusCode, resp.Header.Get("Content-Length"))
	}

	b := &bytes.Buffer{}

	if chunked {
		fmt.Fprintf(b, "HTTP/1.1 %s\r\n", resp.Status)
		resp.Header.Write(b)
		io.WriteString(b, "\r\n")

		rw.Header().Set("Content-Type", "text/plain")
	} else {
		w, _ := flate.NewWriter(b, flate.BestCompression)
		fmt.Fprintf(w, "HTTP/1.1 %s\r\n", resp.Status)
		resp.Header.Write(w)
		io.WriteString(w, "\r\n")
		w.Close()

		b0 := []byte{0, 0}
		binary.BigEndian.PutUint16(b0, uint16(b.Len()))

		rw.Header().Set("Content-Type", "image/gif")
		// we need not set a "Content-Length" header, App Engine will reset it
		//rw.Header().Set("Content-Length", strconv.FormatInt(int64(len(b0)+b.Len())+resp.ContentLength, 10))
		//rw.WriteHeader(http.StatusOK)
		rw.Write(b0)
	}
	io.Copy(rw, io.MultiReader(b, resp.Body))
}

func favicon(rw http.ResponseWriter, r *http.Request) {
	rw.WriteHeader(http.StatusOK)
}

func robots(rw http.ResponseWriter, r *http.Request) {
	rw.Header().Set("Content-Type", "text/plain; charset=utf-8")
	io.WriteString(rw, "User-agent: *\nDisallow: /\n")
}

/*func root(rw http.ResponseWriter, r *http.Request) {
	c := appengine.NewContext(r)

	version, _ := strconv.ParseInt(strings.Split(appengine.VersionID(c), ".")[1], 10, 64)
	ctime := time.Unix(version/(1<<28), 0).Format(time.RFC3339)

	var latest string
	t := &urlfetch.Transport{Context: c}
	req, _ := http.NewRequest("GET", "https://github.com/SeaHOH/GotoX/commits/gaeserver.goproxy/gae", nil)
	resp, err := t.RoundTrip(req)
	if err != nil {
		latest = err.Error()
	} else {
		data, err := ioutil.ReadAll(resp.Body)
		if err != nil {
			latest = err.Error()
		} else {
			latest = regexp.MustCompile(`\d\d\d\d-\d\d-\d\dT\d\d:\d\d:\d\dZ`).FindString(string(data))
		}
	}

	rw.Header().Set("Content-Type", "text/plain; charset=utf-8")
	var message string
	switch {
	case latest == "":
		message = "unable check goproxy latest version, please try after 5 minutes."
	case latest <= ctime:
		message = "already update to latest."
	default:
		message = "please update this server"
	}
	fmt.Fprintf(rw, `{
	"server": "goproxy %s (%s, %s/%s)"
	"latest": "%s",
	"deploy": "%s",
	"message": "%s"
}
`, Version, runtime.Version(), runtime.GOOS, runtime.GOARCH, latest, ctime, message)
}*/
func root(rw http.ResponseWriter, r *http.Request) {
	//c := appengine.NewContext(r)
	//c :=r.Context()

	//version, _ := strconv.ParseInt(strings.Split(appengine.VersionID(c), ".")[1], 10, 64)
	//ctime := time.Unix(version/(1<<28), 0).Format(time.RFC3339)
	var version int64 = ((2020-1970)*365+6*30+18)*24*60*60
	ctime := time.Unix(version, 0).Format(time.RFC3339)

	var latest string
	//t := &urlfetch.Transport{Context: c}
	t := &http.Transport{}; //???
	//var t http.Transport; //???
	req, _ := http.NewRequest("GET", "https://github.com/SeaHOH/GotoX/commits/gaeserver.goproxy/gae", nil)
	resp, err := t.RoundTrip(req)
	if err != nil {
		latest = err.Error()
	} else {
		data, err := ioutil.ReadAll(resp.Body)
		if err != nil {
			latest = err.Error()
		} else {
			latest = regexp.MustCompile(`\d\d\d\d-\d\d-\d\dT\d\d:\d\d:\d\dZ`).FindString(string(data))
		}
	}

	rw.Header().Set("Content-Type", "text/plain; charset=utf-8")
	var message string
	switch {
	case latest == "":
		message = "unable check goproxy latest version, please try after 5 minutes."
	case latest <= ctime:
		message = "already update to latest."
	default:
		message = "please update this server"
	}
	fmt.Fprintf(rw, `{
	"server": "goproxy %s (%s, %s/%s)"
	"latest": "%s",
	"deploy": "%s",
	"message": "%s"
}
`, Version, runtime.Version(), runtime.GOOS, runtime.GOARCH, latest, ctime, message)

    fmt.Fprintf(rw, `{
    "type": "appengine(%s, %s/%s)",
    "host": "%s",
    "software": "%s",
    "port": "%s" ,
    "project_id": "%s"
}
`, runtime.Version(), runtime.GOOS, runtime.GOARCH, "appengine.DefaultVersionHostname(c)", os.Getenv("GAE_ENV"),os.Getenv("PORT"),os.Getenv("GOOGLE_CLOUD_PROJECT"))
   
   env := os.Environ();
   for i := 0;i< len(env);i=i+1{
      fmt.Fprintf(rw,"%v\n",env[i]);
   }
}

/*func init() {
	http.HandleFunc("/_gh/", handler)
	http.HandleFunc("/favicon.ico", favicon)
	http.HandleFunc("/robots.txt", robots)
	http.HandleFunc("/", root)
}*/
func main(){
	//os.Setenv("GODEBUG","http2client=0")
	http.HandleFunc("/_gh/", handler)
	http.HandleFunc("/g", handler)
	http.HandleFunc("/favicon.ico", favicon)
	http.HandleFunc("/robots.txt", robots)
	http.HandleFunc("/", root)
	//appengine.Main()
	//MainPath = filepath.Dir(findMainPath())
	//installHealthChecker(http.DefaultServeMux)
	port := os.Getenv("PORT")
	if port == "" {
	        port = "80"
	        //log.Printf("Defaulting to port %s", port)
	}

        host := ""
	//if IsDevAppServer() {
		//host = "127.0.0.1"
	//}
	//log.Printf("Listening on port %s", port)
	if err := http.ListenAndServe(host+":"+port, nil); err != nil {
	        log.Fatal(err)
	}

	/*if err := http.ListenAndServe(host+":"+port, http.HandlerFunc(handleHTTP)); err != nil {
		log.Fatalf("http.ListenAndServe: %v", err)
	}*/
}
