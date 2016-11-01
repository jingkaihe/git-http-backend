package main

import (
	"compress/gzip"
	"flag"
	"fmt"
	"io"
	"io/ioutil"
	"log"
	"net/http"
	"os"
	"path"
	"regexp"
	"strconv"
	"time"
)

const (
	version     = "0.0.1"
	uploadPack  = "git-upload-pack"
	receivePack = "git-receive-pack"
	banner      = `
       _ _     _   _   _          _             _               _
  __ _(_) |_  | |_| |_| |_ _ __  | |__  __ _ __| |_____ _ _  __| |
 / _` + "` " + `| |  _| | ' \  _|  _| '_ \ | '_ \/ _` + "` " + `/ _| / / -_) ' \/ _` + "`" + ` |
 \__, |_|\__| |_||_\__|\__| .__/ |_.__/\__,_\__|_\_\___|_||_\__,_|
 |___/                    |_|
  Git HTTP Backend
    Version: %s
`
)

// Service defines the Git Smart HTTP request by the given method and pattern
type Service struct {
	Method  string
	Pattern *regexp.Regexp
	Handler func(s Service, w http.ResponseWriter, r *http.Request)
}

// ParseURLNamedParams parse the request into named parameters
func (s *Service) ParseURLNamedParams(r *http.Request) map[string]string {
	namedParams := make(map[string]string)

	subexpNames := s.Pattern.SubexpNames()
	matches := s.Pattern.FindAllStringSubmatch(r.URL.Path, -1)[0]

	for i, match := range matches {
		if name := subexpNames[i]; name != "" {
			namedParams[name] = match
		}
	}
	return namedParams
}

// GitSmartHTTPConfig is the configuration for GitSmartHTTP
type GitSmartHTTPConfig struct {
	ReposRootPath string
	ReceivePack   bool
	UploadPack    bool
	Port          int
}

// GitSmartHTTP acts as an Git Smart HTTP server's handler and deal
// with all kinds of Git HTTP request
type GitSmartHTTP struct {
	Services []Service
	*GitSmartHTTPConfig
}

// NewGitSmartHTTP returns a GitSmartHTTP
func NewGitSmartHTTP(cfg *GitSmartHTTPConfig) GitSmartHTTP {
	gsh := GitSmartHTTP{
		GitSmartHTTPConfig: cfg,
	}

	gsh.Services = []Service{
		Service{
			Method:  "GET",
			Pattern: regexp.MustCompile("(?P<repoPath>.*)/HEAD$"),
			Handler: gsh.handleTextFile,
		},
		Service{
			Method:  "GET",
			Pattern: regexp.MustCompile("(?P<repoPath>.*)/info/packs$"),
			Handler: gsh.handleInfoPacks,
		},
		Service{
			Method:  "GET",
			Pattern: regexp.MustCompile("(?P<repoPath>.*)/info/refs$"),
			Handler: gsh.handleInfoRefs,
		},
		Service{
			Method:  "GET",
			Pattern: regexp.MustCompile("(?P<repoPath>.*)/objects/info/alternates$"),
			Handler: gsh.handleTextFile,
		},
		Service{
			Method:  "GET",
			Pattern: regexp.MustCompile("(?P<repoPath>.*)/objects/info/http-alternates$"),
			Handler: gsh.handleTextFile,
		},
		Service{
			Method:  "GET",
			Pattern: regexp.MustCompile("(?P<repoPath>.*)/objects/[0-9a-f]{2}/[0-9a-f]{38}$"),
			Handler: gsh.handleLooseObject,
		},
		Service{
			Method:  "GET",
			Pattern: regexp.MustCompile("(?P<repoPath>.*)/objects/pack/pack-[0-9a-f]{40}\\.pack$"),
			Handler: gsh.handlePackFile,
		},
		Service{
			Method:  "GET",
			Pattern: regexp.MustCompile("(?P<repoPath>.*)/objects/pack/pack-[0-9a-f]{40}\\.idx$"),
			Handler: gsh.handleIdxFile,
		},
		Service{
			Method:  "POST",
			Pattern: regexp.MustCompile("(?P<repoPath>.*)/(?P<serviceType>git-upload-pack)$"),
			Handler: gsh.handleServiceRPC,
		},
		Service{
			Method:  "POST",
			Pattern: regexp.MustCompile("(?P<repoPath>.*)/(?P<serviceType>git-receive-pack)$"),
			Handler: gsh.handleServiceRPC,
		},
	}
	return gsh
}

// ServerHttp implements the iServerHttp nterface of http.Handler
func (gsh GitSmartHTTP) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	// Log request
	log.Printf(`%s - - "%s %s %s"`, r.RemoteAddr, r.Method, r.URL.Path, r.Proto)

	for _, service := range gsh.Services {
		if service.Pattern.MatchString(r.URL.Path) {
			if r.Method == service.Method {
				service.Handler(service, w, r)
			} else {
				methodNotAllowed(w, r)
			}
			break
		}
	}
}

func (gsh GitSmartHTTP) handleTextFile(s Service, w http.ResponseWriter, r *http.Request) {
	gsh.sendFile(w, r, "text/plain", hdrNoCache())
}

func (gsh GitSmartHTTP) handleInfoPacks(s Service, w http.ResponseWriter, r *http.Request) {
	gsh.sendFile(w, r, "text/plain; charset=utf-8", hdrNoCache())
}

func (gsh GitSmartHTTP) handleLooseObject(s Service, w http.ResponseWriter, r *http.Request) {
	gsh.sendFile(w, r, "application/x-git-loose-object", hdrCacheForever())
}

func (gsh GitSmartHTTP) handlePackFile(s Service, w http.ResponseWriter, r *http.Request) {
	gsh.sendFile(w, r, "application/x-git-packed-objects", hdrCacheForever())
}

func (gsh GitSmartHTTP) handleIdxFile(s Service, w http.ResponseWriter, r *http.Request) {
	gsh.sendFile(w, r, "application/x-git-packed-objects-toc", hdrCacheForever())
}

func (gsh GitSmartHTTP) handleInfoRefs(s Service, w http.ResponseWriter, r *http.Request) {
	defer r.Body.Close()

	serviceType := r.FormValue("service")

	namedURLParams := s.ParseURLNamedParams(r)
	repoPath := path.Join(gsh.ReposRootPath, namedURLParams["repoPath"])

	gs := NewGitRPCClient(&GitRPCClientConfig{
		Stream: false,
	})

	if gsh.serviceAccess(serviceType) {
		w.Header().Add("Content-Type", fmt.Sprintf("application/x-%s-advertisement", serviceType))
		setHeaders(w, hdrNoCache())
		w.WriteHeader(http.StatusOK)

		rpcCfg := map[string]struct{}{
			"advertise_refs": struct{}{},
		}

		if serviceType == uploadPack {
			gs.UploadPack(repoPath, rpcCfg)
		} else {
			gs.ReceivePack(repoPath, rpcCfg)
		}
		refs, _ := gs.Output()

		fmt.Fprint(w, pktWrite(fmt.Sprintf("# service=%s\n", serviceType)))
		fmt.Fprint(w, pktFlush())
		w.Write(refs)
	} else {
		gs.UploadPack(repoPath, map[string]struct{}{})
		gs.Output()

		gsh.sendFile(w, r, "text/plain; charset=utf-8", hdrNoCache())
	}
}

func (gsh GitSmartHTTP) handleServiceRPC(s Service, w http.ResponseWriter, r *http.Request) {
	defer r.Body.Close()

	namedURLParams := s.ParseURLNamedParams(r)

	repoPath := path.Join(gsh.ReposRootPath, namedURLParams["repoPath"])
	serviceType := namedURLParams["serviceType"]

	if !gsh.serviceAccess(serviceType) {
		w.WriteHeader(http.StatusForbidden)
		w.Header().Set("Content-Type", "text/plain")
		return
	}

	var reqBody []byte

	switch r.Header.Get("Content-Encoding") {
	case "gzip":
		reader, err := gzip.NewReader(r.Body)
		if err != nil {
			log.Printf("Cannot parse request body with: %s", err)
		}
		defer reader.Close()
		reqBody, _ = ioutil.ReadAll(reader)
	default:
		reqBody, _ = ioutil.ReadAll(r.Body)
	}

	gs := NewGitRPCClient(&GitRPCClientConfig{
		Stream: true,
	})

	if serviceType == uploadPack {
		gs.UploadPack(repoPath, map[string]struct{}{})
	} else {
		gs.ReceivePack(repoPath, map[string]struct{}{})
	}

	w.Header().Set("Content-Type", fmt.Sprintf("application/x-%s-result", serviceType))

	if err := gs.Start(); err != nil {
		log.Printf("Git RPC call %s cannot be started successfully: %s", serviceType, err)
	}

	gs.StdinWriter.Write(reqBody)
	io.Copy(w, gs.StdoutReader)
	io.Copy(w, gs.StderrReader)

	if err := gs.Wait(); err != nil {
		log.Printf("Git RPC call %s cannot be stopped properly: %s", serviceType, err)
	}
}

func pktWrite(s string) string {
	sSize := strconv.FormatInt(int64(len(s)+4), 16)
	sSize = fmt.Sprintf("%04s", sSize)
	return sSize + s
}

func pktFlush() string {
	return "0000"
}

func (gsh GitSmartHTTP) sendFile(w http.ResponseWriter, r *http.Request, contentType string, hdr map[string]string) {
	fullPath := path.Join(gsh.ReposRootPath, r.URL.Path)

	f, err := os.Open(fullPath)
	if err != nil {
		w.Header().Set("Content-Type", "text/plain")
		http.NotFound(w, r)
		return
	}

	fInfo, err := f.Stat()
	if err != nil {
		fmt.Fprintf(w, "Cannot fetch file %v", err)
		return
	}

	size := strconv.FormatInt(fInfo.Size(), 10)
	mtime := fInfo.ModTime().Format(time.RFC850)

	setHeaders(w, hdr)

	w.Header().Set("Content-Type", contentType)
	w.Header().Set("Content-Length", size)
	w.Header().Set("Last-Modified", mtime)

	io.Copy(w, f)
}

func (gsh GitSmartHTTP) serviceAccess(service string) bool {
	if service == uploadPack {
		return gsh.UploadPack
	}

	if service == receivePack {
		return gsh.ReceivePack
	}

	return false
}

func methodNotAllowed(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/plain")
	if r.Proto == "HTTP/1.1" {
		w.WriteHeader(http.StatusMethodNotAllowed)
	} else {
		w.WriteHeader(http.StatusBadRequest)
	}
}

func hdrNoCache() map[string]string {
	return map[string]string{
		"Expires":       "Fri, 01 Jan 1980 00:00:00 GMT",
		"Pragma":        "no-cache",
		"Cache-Control": "no-cache, max-age=0, must-revalidate",
	}
}

func hdrCacheForever() map[string]string {
	now := time.Now()
	expires := now.Add(31536000 * time.Second)

	return map[string]string{
		"Date":          now.Format(time.RFC850),
		"Expires":       expires.Format(time.RFC850),
		"Cache-Control": "public, max-age=31536000",
	}
}

func setHeaders(w http.ResponseWriter, hdr map[string]string) {
	for key, value := range hdr {
		w.Header().Set(key, value)
	}
}

func main() {
	var vsn bool
	gsc := GitSmartHTTPConfig{}
	flag.BoolVar(&vsn, "version", false, "print version")
	flag.StringVar(&gsc.ReposRootPath, "repo-path", "/etc/git-http-backend", "directory that contains git repositories you want to serve")
	flag.BoolVar(&gsc.ReceivePack, receivePack, true, "whether you want to receive what is pushed into repository")
	flag.BoolVar(&gsc.UploadPack, uploadPack, true, "whether you want to send objects packed back to git-fetch-pack")
	flag.IntVar(&gsc.Port, "port", 8080, "port that the Git server backend runs on")
	flag.Usage = func() {
		fmt.Fprint(os.Stderr, fmt.Sprintf(banner, version))
		flag.PrintDefaults()
	}

	flag.Parse()

	if vsn {
		fmt.Printf("git-http-backend version: %s\n", version)
		os.Exit(0)
	}

	gsh := NewGitSmartHTTP(&gsc)

	mux := http.NewServeMux()
	mux.Handle("/", gsh)
	port := fmt.Sprintf(":%d", gsh.Port)
	log.Printf(banner+"    Running on port %d", version, gsh.Port)
	http.ListenAndServe(port, mux)
}
