package main

import (
	"bytes"
	"compress/gzip"
	"crypto/md5"
	"encoding/json"
	"flag"
	"fmt"
	"io/ioutil"
	"log"
	"net/http"
	"os"
	"os/signal"
	"path"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"
)

var optAddr = flag.String("addr", ":8000", "address to listen on")
var optPath = flag.String("path", ".", "path of the static files to serve")
var optStaticDir = flag.String("public", "public", "name of the 'public' directory")
var optTemplate = flag.String("tmpl", "template.html", "template to use")
var optHomeDir = flag.String("home", "", "home directory")
var optDefault = flag.String("default", "index", "default file to render")
var optExt = flag.String("ext", "md", "extension of the markdown files")

var root string
var cache map[string]*Page
var indexCache map[string][]PageIndex
var tmpl [][]byte
var tmplHash []byte

func main() {
	flag.Parse()

	var err error
	root, err = filepath.Abs(*optPath)

	if err != nil {
		log.Fatalf("Could not get the absolute path of %v. %v", *optPath, err)
	}

	setupStaticDir()
	setupTemplate()
	setupSignals()
  setupIndexes()

	http.HandleFunc("/", renderPage)

	err = http.ListenAndServe(*optAddr, nil)

	if err != nil {
		log.Fatalf("Could not serve static files at path %v. %v", root, err)
	}
}

type PageIndex struct {
  Name string
  Date time.Time
  Summary string
  Tags []string
}

func setupIndexes() {
  indexCache = make(map[string][]PageIndex)

  filepath.Walk(root, func(path string, info os.FileInfo, err error) error {
    if info.IsDir() && strings.HasPrefix(info.Name(), ".") {
      return filepath.SkipDir
    }

    if !info.IsDir() && info.Name() == *optDefault + ".json" {
      f, err := os.Open(path)
      if err != nil {
        log.Fatalf("Couldn't open %v for reading: %v", path, err)
        return err
      }

      defer f.Close()

      var pages []PageIndex

      jsdec := json.NewDecoder(f)
      err = jsdec.Decode(&pages)

      if err != nil {
        log.Fatalf("Couldn't parse %v as an index file: %v", path, err)
        return err
      }

      indexCache[path] = pages
    }

    return nil
  })
}

func setupStaticDir() {
	static := "/" + *optStaticDir + "/"
	public := filepath.Join(root, *optStaticDir)

	h := http.StripPrefix(static, http.FileServer(http.Dir(public)))

	http.Handle(static, h)
	http.HandleFunc("/favicon.ico", func(w http.ResponseWriter, r *http.Request) {
		http.ServeFile(w, r, filepath.Join(public, "favicon.ico"))
	})
	http.HandleFunc("/robots.txt", func(w http.ResponseWriter, r *http.Request) {
		http.ServeFile(w, r, filepath.Join(public, "robots.txt"))
	})
}

func setupTemplate() {
	path := filepath.Join(root, *optTemplate)
	b, err := ioutil.ReadFile(path)

	if err != nil {
		log.Fatal("Could not read template: ", err)
	}

	tmplHash = hash(b)
	tmpl = bytes.Split(b, []byte("{{content}}"))

	if len(tmpl) != 2 {
		log.Fatal("Template was not in a valid format")
	}

	cache = make(map[string]*Page)
}

func setupSignals() {
	c := make(chan os.Signal, 1)
	signal.Notify(c, syscall.SIGUSR1)

	go func() {
		<-c
		setupTemplate()
		log.Println("SIGUSR1: Template and page cache cleared.")
	}()
}

func renderPage(w http.ResponseWriter, r *http.Request) {
	p, file := path.Split(r.URL.Path)

	if p == "" {
		p = *optHomeDir
	}

	if file == "" {
		file = *optDefault
	}

	abs := path.Join(root, p, file)

  if pIdx, ok := indexCache[abs + ".json"]; ok {
    renderIndex(w, r, pIdx)
    return
  }

  abs = abs + "." + *optExt

	var page *Page
	var ok bool
	var err error

	if page, ok = cache[abs]; !ok {
		page, err = GetPage(abs)

		if err != nil {
			log.Printf("[%v] %v", abs, err)
			writeError(w, r, err)
			return
		}

		cache[abs] = page
	}

	write(w, r, page)
}

func renderIndex(w http.ResponseWriter, r *http.Request, pIdx []PageIndex) {
  w.Write(tmpl[0])
  fmt.Fprintln(w, "<h2>Blog</h2>")
  for i := 0; i < len(pIdx); i++ {
    fmt.Fprintf(w, "<h3>%v</h3>\n<p>%v</p>\n", pIdx[i].Name, pIdx[i].Summary)
  }

  w.Write(tmpl[1])
}

func write(w http.ResponseWriter, r *http.Request, bytes []byte, hash []byte) {
  if len(hash) > 0 {
    if etag := r.Header.Get("If-None-Match"); strings.EqualFold(etag, hash) {
      w.WriteHeader(http.StatusNotModified)
      return
    }

    w.Header().Set("Etag", hash)
  }

	w.Header().Set("Content-Type", "text/html; charset=UTF-8")

	if strings.Contains(r.Header.Get("Accept-Encoding"), "gzip") {
		b := new(bytes.Buffer)
		gz := gzip.NewWriter(b)
		gz.Write(tmpl[0])
		gz.Write(page.Content)
		gz.Write(tmpl[1])
		gz.Close()

		w.Header().Set("Content-Encoding", "gzip")
		w.Header().Set("Content-Length", strconv.Itoa(b.Len()))

		b.WriteTo(w)
	} else {
		w.Header().Set("Content-Length", strconv.Itoa(len(tmpl[0])+len(page.Content)+len(tmpl[1])))
		w.Write(tmpl[0])
		w.Write(page.Content)
		w.Write(tmpl[1])
	}
}

func writeError(w http.ResponseWriter, r *http.Request, err error) {
	errFmt := "<h2>Oops! We've hit a bit of a problem...</h2><p>%v</p>"

	w.Header().Set("Content-Type", "text/html; charset=UTF-8")

	b := new(bytes.Buffer)
	b.Write(tmpl[0])

	if pErr, ok := err.(*PageError); ok {
		w.WriteHeader(pErr.StatusCode)
		fmt.Fprintf(b, errFmt, pErr.Message)
	} else {
		w.WriteHeader(http.StatusInternalServerError)
		fmt.Fprintf(b, errFmt, "Page not available")
	}

	b.Write(tmpl[1])
	b.WriteTo(w)
}

func hash(value []byte) []byte {
	h := md5.New()
	h.Write(value)
	return h.Sum(nil)
}
