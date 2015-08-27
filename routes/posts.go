package routes

import (
	"bufio"
	"log"
	"net/http"
	"strings"

	. "github.com/9uuso/vertigo/databases/gorm"
	. "github.com/9uuso/vertigo/misc"
	. "github.com/9uuso/vertigo/settings"
	"vertigo/render"

	"github.com/9uuso/go-jaro-winkler-distance"
	"github.com/go-martini/martini"
	"github.com/martini-contrib/sessions"
)

// Homepage route fetches all posts from database and renders them according to "home.tmpl".
// Normally you'd use this function as your "/" route.
func Homepage(w http.ResponseWriter, r *http.Request) {
	if Settings.Firstrun {
		render.R.HTML(w, 200, "installation/wizard", nil)
		return
	}
	var post Post
	posts, err := post.GetAll()
	if err != nil {
		log.Println(err)
		render.R.JSON(w, 500, map[string]interface{}{"error": "Internal server error"})
		return
	}
	render.R.HTML(w, 200, "home", posts)
}

// Search struct is basically just a type check to make sure people don't add anything nasty to
// on-site search queries.
type Search struct {
	Query string `json:"query" form:"query" binding:"required"`
	Score float64
	Posts []Post
}

// Get or search.Get returns all posts which contain parameter search.Query in either
// post.Title or post.Content.
// Returns []Post and error object.
func (search Search) Get() (Search, error) {
	var post Post
	posts, err := post.GetAll()
	if err != nil {
		log.Println(err)
		return search, err
	}
	for _, post := range posts {
		if post.Published {
			// posts are searched for a match in both content and title, so here
			// we declare two scanners for them
			content := bufio.NewScanner(strings.NewReader(post.Markdown))
			title := bufio.NewScanner(strings.NewReader(post.Title))
			// Blackfriday makes smartypants corrections some characters, which break the search
			content.Split(bufio.ScanWords)
			title.Split(bufio.ScanWords)
			// content is scanned trough Jaro-Winkler distance with
			// quite strict matching score of 0.9/1
			// matching score this high would most likely catch only different
			// capitalization and small typos
			//
			// since we are already in a for loop, we have to break the
			// iteration here by going to label End to avoid showing a
			// duplicate search result
			for content.Scan() {
				if jwd.Calculate(content.Text(), search.Query) >= 0.9 {
					search.Posts = append(search.Posts, post)
					goto End
				}
			}
			for title.Scan() {
				if jwd.Calculate(title.Text(), search.Query) >= 0.9 {
					search.Posts = append(search.Posts, post)
					goto End
				}
			}
		}
	End:
	}
	if len(search.Posts) == 0 {
		search.Posts = make([]Post, 0)
	}
	return search, nil
}

// SearchPost is a route which returns all posts and aggregates the ones which contain
// the POSTed search query in either Title or Content field.
func SearchPost(w http.ResponseWriter, r *http.Request, search Search) {
	search, err := search.Get()
	if err != nil {
		log.Println(err)
		render.R.JSON(w, 500, map[string]interface{}{"error": "Internal server error"})
		return
	}
	switch Root(r) {
	case "api":
		render.R.JSON(w, 200, search.Posts)
		return
	case "post":
		render.R.HTML(w, 200, "search", search.Posts)
		return
	}
}

// CreatePost is a route which creates a new post according to the posted data.
// API renderponse contains the created post object and normal request redirects to "/user" page.
// Does not publish the post automatically. See PublishPost for more.
func CreatePost(w http.ResponseWriter, r *http.Request, s sessions.Session, post Post) {
	post, err := post.Insert(s)
	if err != nil {
		log.Println(err)
		render.R.JSON(w, 500, map[string]interface{}{"error": "Internal server error"})
		return
	}
	switch Root(r) {
	case "api":
		render.R.JSON(w, 200, post)
		return
	case "post":
		http.Redirect(w, r, "/user", 302)
		return
	}
}

// ReadPosts is a route which returns all posts without merged owner data (although the object does include author field)
// Not available on frontend, so therefore it only returns a JSON renderponse, hence the post iteration in Go.
func ReadPosts(w http.ResponseWriter, r *http.Request) {
	var post Post
	published := make([]Post, 0)
	posts, err := post.GetAll()
	if err != nil {
		log.Println(err)
		render.R.JSON(w, 500, map[string]interface{}{"error": "Internal server error"})
		return
	}
	for _, post := range posts {
		if post.Published {
			published = append(published, post)
		}
	}
	render.R.JSON(w, 200, published)
}

// ReadPost is a route which returns post with given post.Slug.
// Returns post data on JSON call and displays a formatted page on frontend.
func ReadPost(w http.ResponseWriter, r *http.Request, s sessions.Session, params martini.Params) {
	var post Post
	if params["slug"] == "new" {
		render.R.JSON(w, 400, map[string]interface{}{"error": "There can't be a post called 'new'."})
		return
	}
	post.Slug = params["slug"]
	post, err := post.Get()
	if err != nil {
		log.Println(err)
		if err.Error() == "not found" {
			render.R.JSON(w, 404, map[string]interface{}{"error": "Not found"})
			return
		}
		render.R.JSON(w, 500, map[string]interface{}{"error": "Internal server error"})
		return
	}
	go post.Increment()
	switch Root(r) {
	case "api":
		render.R.JSON(w, 200, post)
		return
	case "post":
		render.R.HTML(w, 200, "post/display", post)
		return
	}
}

// EditPost is a route which returns a post object to be displayed and edited on frontend.
// Not available for JSON API.
// Analogous to ReadPost. Could be replaced at some point.
func EditPost(w http.ResponseWriter, r *http.Request, params martini.Params) {
	var post Post
	post.Slug = params["slug"]
	post, err := post.Get()
	if err != nil {
		log.Println(err)
		render.R.JSON(w, 500, map[string]interface{}{"error": "Internal server error"})
		return
	}
	render.R.HTML(w, 200, "post/edit", post)
}

// UpdatePost is a route which updates a post defined by martini parameter "title" with posted data.
// Requirender session cookie. JSON request returns the updated post object, frontend call will redirect to "/user".
func UpdatePost(w http.ResponseWriter, r *http.Request, params martini.Params, s sessions.Session, entry Post) {
	var post Post
	post.Slug = params["slug"]
	post, err := post.Get()
	if err != nil {
		log.Println(err)
		if err.Error() == "not found" {
			render.R.JSON(w, 404, map[string]interface{}{"error": "Not found"})
			return
		}
		render.R.JSON(w, 500, map[string]interface{}{"error": "Internal server error"})
		return
	}

	post, err = post.Update(s, entry)
	if err != nil {
		log.Println(err)
		if err.Error() == "unauthorized" {
			render.R.JSON(w, 401, map[string]interface{}{"error": "Unauthorized"})
			return
		}
		render.R.JSON(w, 500, map[string]interface{}{"error": "Internal server error"})
		return
	}

	switch Root(r) {
	case "api":
		render.R.JSON(w, 200, post)
		return
	case "post":
		http.Redirect(w, r, "/user", 302)
		return
	}
}

// PublishPost is a route which publishes a post and therefore making it appear on frontpage and search.
// JSON request returns `HTTP 200 {"success": "Post published"}` on success. Frontend call will redirect to
// published page.
// Requirender active session cookie.
func PublishPost(w http.ResponseWriter, r *http.Request, params martini.Params, s sessions.Session) {
	var post Post
	post.Slug = params["slug"]
	post, err := post.Get()
	if err != nil {
		log.Println(err)
		if err.Error() == "not found" {
			render.R.JSON(w, 404, map[string]interface{}{"error": "Not found"})
			return
		}
		render.R.JSON(w, 500, map[string]interface{}{"error": "Internal server error"})
		return
	}

	var entry Post
	entry = post
	entry.Published = true
	post, err = post.Update(s, entry)
	if err != nil {
		log.Println(err)
		if err.Error() == "unauthorized" {
			render.R.JSON(w, 401, map[string]interface{}{"error": "Unauthorized"})
			return
		}
		render.R.JSON(w, 500, map[string]interface{}{"error": "Internal server error"})
		return
	}

	switch Root(r) {
	case "api":
		render.R.JSON(w, 200, map[string]interface{}{"success": "Post published"})
		return
	case "post":
		http.Redirect(w, r, "/post/"+post.Slug, 302)
		return
	}
}

// UnpublishPost is a route which unpublishes a post and therefore making it disappear from frontpage and search.
// JSON request returns `HTTP 200 {"success": "Post unpublished"}` on success. Frontend call will redirect to
// user control panel.
// Requirender active session cookie.
// The route is anecdotal to route PublishPost().
func UnpublishPost(w http.ResponseWriter, r *http.Request, params martini.Params, s sessions.Session) {
	var post Post
	post.Slug = params["slug"]
	post, err := post.Get()
	if err != nil {
		if err.Error() == "not found" {
			render.R.JSON(w, 404, map[string]interface{}{"error": "Not found"})
			return
		}
		render.R.JSON(w, 500, map[string]interface{}{"error": "Internal server error"})
		return
	}

	err = post.Unpublish(s)
	if err != nil {
		log.Println(err)
		if err.Error() == "unauthorized" {
			render.R.JSON(w, 401, map[string]interface{}{"error": "Unauthorized"})
			return
		}
		render.R.JSON(w, 500, map[string]interface{}{"error": "Internal server error"})
		return
	}

	switch Root(r) {
	case "api":
		render.R.JSON(w, 200, map[string]interface{}{"success": "Post unpublished"})
		return
	case "post":
		http.Redirect(w, r, "/user", 302)
		return
	}
}

// DeletePost is a route which deletes a post according to martini parameter "title".
// JSON request returns `HTTP 200 {"success": "Post deleted"}` on success. Frontend call will redirect to
// "/user" page on successful request.
// Requirender active session cookie.
func DeletePost(w http.ResponseWriter, r *http.Request, params martini.Params, s sessions.Session) {
	var post Post
	post.Slug = params["slug"]
	post, err := post.Get()
	if err != nil {
		if err.Error() == "not found" {
			render.R.JSON(w, 404, map[string]interface{}{"error": "Not found"})
			return
		}
		log.Println(err)
		render.R.JSON(w, 500, map[string]interface{}{"error": "Internal server error"})
		return
	}
	err = post.Delete(s)
	if err != nil {
		log.Println(err)
		if err.Error() == "unauthorized" {
			render.R.JSON(w, 401, map[string]interface{}{"error": "Unauthorized"})
			return
		}
		render.R.JSON(w, 500, map[string]interface{}{"error": "Internal server error"})
		return
	}
	switch Root(r) {
	case "api":
		render.R.JSON(w, 200, map[string]interface{}{"success": "Post deleted"})
		return
	case "post":
		http.Redirect(w, r, "/user", 302)
		return
	}
}
