package main

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"github.com/PuerkitoBio/goquery"
	"github.com/bgentry/speakeasy"
	"github.com/blevesearch/bleve"
	"github.com/mitchellh/go-homedir"
	"github.com/rakyll/globalconf"
	"github.com/toqueteos/webbrowser"
	"html/template"
	"io"
	"log"
	"net/http"
	"net/url"
	"os"
	"regexp"
	"strconv"
	"strings"
	"time"
)

type PageIndex struct {
	Title string
}

type PageError struct {
	Title     string
	ErrorCode int
	ErrorDesc string
}

type PM struct {
	Id    string
	To    string
	From  string
	Date  string
	Title string
	Msg   template.HTML
}

type Hits []PM

type Result struct {
	Hits []PM
	From int
	Curr template.HTML
	Prev template.HTML
	Next template.HTML
}

type Search struct {
	Text string `json:"search"`
	From int    `json:"from"`
}

var (
	conf       *globalconf.GlobalConf
	templates  *template.Template
	index      bleve.Index
	logged_in  = false
	session_id = ""
	base_path  = "" // base path
	pass_local = ""
	pass_key   = []byte("a687bf46d4bd2a2b07c36bd76e61eb40") // 32 bytes
	user       = flag.String("user", "", "Your GH username")
	pass       = flag.String("pass", "", "Your GH password")
	pass_hash  = flag.String("pass_hash", "", "Dynamic: Do not modify this...")
	port       = flag.Int("port", 8888, "The port the pm_search app should listen on")
	page_size  = flag.Int("page_size", 50, "Number of PMs to show on each page")
	stored_pm  = flag.Int("stored_pm", 0, "Dynamic: Last indexed PM.  To re-index, set to: 0")
)

func main() {
	var err error
	home_dir, _ := homedir.Dir()
	base_path = fmt.Sprintf("%s%s%s%s", home_dir, string(os.PathSeparator), ".pm_search", string(os.PathSeparator))
	err = os.MkdirAll(base_path, 0777)
	if err != nil {
		fmt.Println("\nCould not create directory:%s\nError:%s\n", base_path, err.Error())
		os.Exit(1)
	}
	bleve_path := fmt.Sprintf("%s%s", base_path, "pm_search.bleve")

	log_path := fmt.Sprintf("%s%s", base_path, "pm_search.log")
	log_file, err := os.OpenFile(log_path, os.O_RDWR|os.O_CREATE|os.O_APPEND, 0666)
	if err != nil {
		fmt.Printf("Error creating log: '%s' - %s\n", log_path, err.Error())
	}
	defer log_file.Close()
	log.SetOutput(log_file)

	// setup config import
	conf_path := fmt.Sprintf("%s%s", base_path, "pm_search.conf")
	conf_file, err := os.OpenFile(conf_path, os.O_RDWR|os.O_CREATE|os.O_APPEND, 0666) // create if needed
	if err != nil {
		log.Printf("Error creating config file: '%s' - %s\n", err.Error())
	}
	conf_file.Close() // close right away and give control to globalconf now that we have a file for sure
	conf, err = globalconf.NewWithOptions(&globalconf.Options{
		Filename: conf_path,
	})
	conf.ParseAll()

	// check if we have a 'user'
	if *user == "" {
		fmt.Println("\n'-user' is required\n")
		flag.PrintDefaults()
		os.Exit(1)
	} else {
		conf.Set("", flag.Lookup("user")) // store value to the config file
	}

	// ask for the pass via the command line and don't save it
	if *pass == "" && *pass_hash == "" {
		pass_local, err = speakeasy.Ask("Enter your GH password: ")
		if err != nil {
			fmt.Printf("Password Error: %s\n", err.Error())
			os.Exit(1)
		}
	}

	// check if they have entered a new password
	if *pass != "" {
		pass_local = *pass
		secure_pass, err := encrypt(pass_key, []byte(pass_local))
		if err != nil {
			log.Printf("Password could not be encrypted: %s\n", err.Error())
		} else {
			flag.Set("pass_hash", hex.EncodeToString(secure_pass)) // set secure pass in config
			flag.Set("pass", "")                                   // remove pass from config
			conf.Set("", flag.Lookup("pass_hash"))                 // store value to the config file
			conf.Set("", flag.Lookup("pass"))                      // store value to the config file
		}
	}

	// check if they have a hashed password
	if *pass_hash != "" {
		pass_decode, err := hex.DecodeString(*pass_hash)
		if err != nil {
			log.Printf("Password could not be decoded: %s\n", err.Error())
		}
		pass_bytes, err := decrypt(pass_key, pass_decode)
		if err != nil {
			log.Printf("Password could not be decrpyted: %s\n", err.Error())
		}
		pass_local = string(pass_bytes)
	}

	// setup bleve
	mapping := bleve.NewIndexMapping()
	index, err = bleve.New(bleve_path, mapping)
	if err != nil {
		index, err = bleve.Open(bleve_path)
		if err != nil {
			log.Fatal(err.Error())
		}
	}

	// setup template
	tpl_path, err := FSString(false, "/static/views/templates.html")
	if err != nil {
		log.Printf("Error creating templates: %s\n", err.Error())
	}
	func_map := template.FuncMap{
		//"raw": template.HTML(),
		"raw": func(msg interface{}) template.HTML { return template.HTML(msg.(template.HTML)) },
	}
	templates = template.Must(template.New("").Funcs(func_map).Parse(tpl_path))

	// handle the url routing
	http.HandleFunc("/crawl_pms", handleCrawl)
	http.HandleFunc("/search", handleSearch)
	http.HandleFunc("/", handleIndex)
	http.Handle("/static/", http.FileServer(FS(false)))

	// serve single root level files
	handleSingle("/favicon.ico", "/static/img/favicon.ico")

	// open a web browser in 100ms from now
	go func(port int) {
		time.Sleep(100 * time.Millisecond)
		webbrowser.Open(fmt.Sprintf("http://localhost:%d", port))
	}(*port)

	log.Printf("pm_search started - localhost:%d\n", *port)
	http.ListenAndServe(fmt.Sprintf(":%d", *port), nil)
}

func handleIndex(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/" {
		handleError(w, r, 404, "The page you are looking for does not exist.")
		return
	}

	page := &PageIndex{
		Title: "GH PM Search",
	}

	// render the page...
	if err := templates.ExecuteTemplate(w, "index", page); err != nil {
		log.Printf("Error executing template: %s\n", err.Error())
		http.Error(w, err.Error(), http.StatusInternalServerError)
	}
	return
}

func handleCrawl(w http.ResponseWriter, r *http.Request) {
	login()     // make sure we have a valid session
	crawl_pms() // get pms from gh
}

func handleSearch(w http.ResponseWriter, r *http.Request) {
	decoder := json.NewDecoder(r.Body)
	var post Search
	err := decoder.Decode(&post)
	if err != nil {
		log.Printf("Error decoding search: %s\n", err.Error())
	}
	if strings.TrimSpace(post.Text) == "" {
		return
	}
	if post.From < 0 {
		post.From = 0
	}

	query := bleve.NewQueryStringQuery(strings.TrimSpace(post.Text))
	search := bleve.NewSearchRequest(query)
	search.Size = *page_size
	search.From = post.From
	search.Fields = []string{"*"} // get all fields
	result, err := index.Search(search)
	if err != nil {
		log.Printf("Error while searching: %s\n", err.Error())
	}
	hits := make([]PM, 0)
	for _, hit := range result.Hits {
		hits = append(hits, PM{
			Id:    hit.Fields["Id"].(string),
			To:    hit.Fields["To"].(string),
			From:  hit.Fields["From"].(string),
			Date:  hit.Fields["Date"].(string),
			Title: hit.Fields["Title"].(string),
			Msg:   template.HTML(hit.Fields["Msg"].(string)),
		})
	}
	lower := post.From // initialize to lowest possible number
	if result.Total > 0 {
		lower = post.From + 1 // increment to first entry if there are entries
	}
	var upper int
	if result.Total < uint64(*page_size+post.From) {
		upper = int(result.Total)
	} else {
		upper = *page_size + post.From
	}
	curr := template.HTML(fmt.Sprintf("<li class=\"showing\">%d-%d / %d</li>", lower, upper, result.Total))

	var prev_from int
	if post.From-*page_size < 0 {
		prev_from = 0
	} else {
		prev_from = post.From - *page_size
	}
	var prev_class string
	prev_click := fmt.Sprintf("window.search(%d);", prev_from)
	if post.From == 0 {
		prev_class = " class=\"disabled\""
		prev_click = fmt.Sprintf("javascript:void(0);")
	}
	prev := template.HTML(fmt.Sprintf("<li%s><a onclick=\"%s\" href=\"javascript:void(0);\">Previous</a></li>",
		prev_class, prev_click))

	var next_class string
	next_click := fmt.Sprintf("window.search(%d);", upper)
	if upper == int(result.Total) {
		next_class = " class=\"disabled\""
		next_click = fmt.Sprintf("javascript:void(0);")
	}
	next := template.HTML(fmt.Sprintf("<li%s><a onclick=\"%s\" href=\"javascript:void(0);\">Next</a></li>",
		next_class, next_click))
	results := &Result{
		Hits: hits,
		From: post.From,
		Curr: curr,
		Prev: prev,
		Next: next,
	}

	// render the page...
	if err := templates.ExecuteTemplate(w, "results", results); err != nil {
		log.Printf("Error executing template: %s\n", err.Error())
		http.Error(w, err.Error(), http.StatusInternalServerError)
	}
	return
}

func handleSingle(pattern string, filename string) {
	http.HandleFunc(pattern, func(w http.ResponseWriter, r *http.Request) {
		b, err := FSByte(false, filename)
		if err != nil {
			log.Printf("Error serving single file: %s\n %s\n", filename, err.Error())
		}
		w.Write(b)
	})
}

func handleError(w http.ResponseWriter, r *http.Request, status int, desc string) {
	title := http.StatusText(status)
	if title == "" {
		title = "Unknown Error"
	}
	page := &PageError{
		Title:     title,
		ErrorCode: status,
		ErrorDesc: desc,
	}
	w.WriteHeader(page.ErrorCode)
	if err := templates.ExecuteTemplate(w, "error", page); err != nil {
		http.Error(w, page.ErrorDesc, page.ErrorCode)
	}
}

func crawl_pms() {
	start := time.Now()

	pms_count := 0
	url_count := 0
	newest_pm := *stored_pm
	new_pms_count := 0
	threshold := 3 // number of pm threads to check before agreeing there are no new pms since last stored
	thresh_count := 0
	thresh_met := false
	for _, parent := range []string{gh_url("action=pm"), gh_url("action=pm;f=sent")} {
		doc, err := goquery.NewDocument(parent)
		if err != nil {
			log.Printf("Error connecting to GH: %s\n", err.Error())
		}

		for {
			// get the urls to loop through on the current page
			urls := []string{""} // add "" at beginning to not reload the current doc before scan
			doc.Find("#personal_messages form .table_grid tr").Each(func(i int, r *goquery.Selection) {
				r.Find("td").Eq(2).Find("a").Each(func(j int, a *goquery.Selection) {
					url, exists := a.Attr("href")
					if exists && !strings.HasPrefix(url, "#") {
						urls = append(urls, url)
					}
				})
			})
			url_count = url_count + len(urls)

			// crawl the page for each url
			batch := index.NewBatch()
			for _, url := range urls {
				if thresh_count == threshold {
					thresh_met = true
					break
				} else {
					thresh_count = thresh_count + 1
				}
				var err error
				if url != "" {
					doc, err = goquery.NewDocument(url)
					if err != nil {
						log.Printf("Error '%s' connecting to: %s\n", err.Error(), url)
					}
				}
				doc.Find("#personal_messages form").Each(func(i int, f *goquery.Selection) {
					f.ChildrenFiltered(".windowbg, .windowbg2").Each(func(j int, s *goquery.Selection) {
						pm := &PM{}
						pm.From = strings.TrimSpace(s.Find(".poster h4 a").Text())
						var to []string
						s.Find(".postarea .keyinfo .smalltext a").Each(func(k int, t *goquery.Selection) {
							to = append(to, strings.TrimSpace(t.Text()))
						})
						pm.To = strings.Join(to, ", ")
						pm.Title = strings.TrimSpace(s.Find(".postarea .keyinfo h5").Text())
						msg, _ := s.Find(".postarea .post .inner").Html()
						pm.Msg = template.HTML(msg)

						// parse date: ... Tue, 13 October 2015, 23:55:50 ...
						date_text := strings.TrimSpace(s.Find(".postarea .keyinfo .smalltext").Text())
						search, _ := regexp.Compile(`\w+,\s\d+\s\w+\s\d+,\s\d+:\d+:\d+`)
						pm.Date = search.FindString(date_text)

						// find a unique id
						id, exists := s.Find(".postarea .keyinfo h5").Attr("id")
						if exists {
							pm.Id = strings.TrimPrefix(id, "subject_")
						} else {
							id, exists = s.Find(".postarea .post .inner").Attr("id")
							if exists {
								pm.Id = strings.TrimPrefix(id, "msg_")
							} else {
								pm.Id = fmt.Sprintf("%s|%s|%s", pm.From, pm.To, pm.Date)
							}
						}
						current_pm, _ := strconv.Atoi(pm.Id)
						if current_pm > newest_pm { // update newest pm
							newest_pm = current_pm
						}
						if current_pm > *stored_pm { // check if the pm is newer than stored pm
							new_pms_count = new_pms_count + 1
							thresh_count = 0
						}
						//log.Printf("REAL: %+v\n\n", *pm)

						// add data to index
						batch.Index(pm.Id, *pm)
						pms_count = pms_count + 1 // increase count of PMs seen...
					})
				})
			}
			err = index.Batch(batch)
			if err != nil {
				log.Printf("Error adding to index: %s\n", err.Error())
			}
			//log.Printf("NEW PMS: %d\n", new_pms_count)

			if thresh_met { // we have tried the required number of urls
				break // we broke out of inner loop
			}

			// check if there are more pages
			n := doc.Find("#personal_messages form .pagesection .floatleft strong").First().NextAllFiltered("a")
			if n.Length() > 0 {
				url, exists := n.First().Attr("href")
				if exists {
					var err error
					doc, err = goquery.NewDocument(url)
					if err != nil {
						log.Printf("Error '%s' connecting to: %s\n", err.Error(), url)
					}
				}
			} else {
				break
			}
		}
	}
	//log.Printf("FINISHED!\n")
	log.Printf("CRAWL TIME: %s\n", time.Since(start))
	flag.Set("stored_pm", strconv.Itoa(newest_pm)) // set flag value in memory
	conf.Set("", flag.Lookup("stored_pm"))         // update the stored_pm on disk
}

func login() {
	// check if logged in
	if session_id != "" {
		logged_in = false // reset to false to be sure...
		doc, err := goquery.NewDocument(gh_url(""))
		if err != nil {
			log.Printf("Error connecting to GH: %s\n", err.Error())
		}
		doc.Find("#button_logout a").Each(func(i int, s *goquery.Selection) {
			href, _ := s.Attr("href")
			link, _ := url.Parse(href)
			params := link.Query()
			if params.Get("action") == "logout" {
				logged_in = true
			}
		})
	}

	// login
	if !logged_in {
		login, err := http.PostForm(gh_url("action=login2"), url.Values{
			"user":    {*user},
			"passwrd": {pass_local},
			//"cookieneverexp": {"on"},
		})
		if err != nil {
			log.Printf("Error posting the login credentials: %s\n", err.Error())
		}
		defer login.Body.Close()

		login_doc, err := goquery.NewDocumentFromReader(io.Reader(login.Body))
		if err != nil {
			log.Printf("Error building query document: %s\n", err.Error())
		}

		login_doc.Find("#button_logout a").Each(func(i int, s *goquery.Selection) {
			href, _ := s.Attr("href")
			link, _ := url.Parse(href)
			params := link.Query()
			if params.Get("PHPSESSID") != "" {
				session_id = params.Get("PHPSESSID")
			}
			if params.Get("action") == "logout" {
				logged_in = true
			}
		})
	}
}

func gh_url(query string) string {
	_url, _ := url.Parse("https://geekhack.org/index.php")
	_params, _ := url.ParseQuery(query)
	if _params.Get("PHPSESSID") == "" && session_id != "" {
		_params.Set("PHPSESSID", session_id)
	}
	_url.RawQuery = _params.Encode()
	return _url.String()
}

func encrypt(key, text []byte) ([]byte, error) {
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	b := base64.StdEncoding.EncodeToString(text)
	ciphertext := make([]byte, aes.BlockSize+len(b))
	iv := ciphertext[:aes.BlockSize]
	if _, err := io.ReadFull(rand.Reader, iv); err != nil {
		return nil, err
	}
	cfb := cipher.NewCFBEncrypter(block, iv)
	cfb.XORKeyStream(ciphertext[aes.BlockSize:], []byte(b))
	return ciphertext, nil
}

func decrypt(key, text []byte) ([]byte, error) {
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	if len(text) < aes.BlockSize {
		return nil, errors.New("ciphertext too short")
	}
	iv := text[:aes.BlockSize]
	text = text[aes.BlockSize:]
	cfb := cipher.NewCFBDecrypter(block, iv)
	cfb.XORKeyStream(text, text)
	data, err := base64.StdEncoding.DecodeString(string(text))
	if err != nil {
		return nil, err
	}
	return data, nil
}
