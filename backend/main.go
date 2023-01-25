package main

import (
	"context"
	"database/sql" // New import
	"encoding/json"
	"fmt"
	_ "github.com/jackc/pgx/v5/stdlib"
	gogpt "github.com/sashabaranov/go-gpt3"
	"github.com/tomasen/realip"
	"golang.org/x/time/rate"
	"log"
	"net/http"
	"os"
	"strings"
	"sync"
	"time" // New import
)

var db *sql.DB

func main() {
	port := ":9999"

	db, _ = openDB()
	defer db.Close()

	http.HandleFunc("/chat", Middleware(rateLimit(ChatAPI)))
	http.HandleFunc("/use", Middleware(UsageAPI))

	fmt.Println("listening on" + port)
	fmt.Println("http://localhost" + port)
	http.ListenAndServe(port, nil)
}

// Allows foreign sites to access Data
func Middleware(fn http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Access-Control-Allow-Headers", "Content-Type, Authentication")
		w.Header().Set("Access-Control-Allow-Origin", "https://aotherapy.com")
		w.Header().Set("Access-Control-Allow-Credentials", "true")
		w.Header().Set("Access-Control-Allow-Methods", "POST, GET, OPTIONS, PUT, DELETE")
		fn.ServeHTTP(w, r)
	}
}

func ChatAPI(w http.ResponseWriter, r *http.Request) {
	letter := `
You are a therapist who is very knowledgable in psychotherapy, good at managing conversations with people and to treat them systematically. It’s also very compassionate and acknowledges the client’s feelings and thoughts without judgement.
Always respond in first person. Try to help the client by giving them advice. Don't repeat the client and words.
Here is one account of how you handled a particular client: 
Chat Bot: It sounds like there are several things you're struggling with and would like to be different in your life. You've been feeling badly about your body for quite some time and would like to feel better about it. It also sounds like you experience a lot of worry and anxiety that is very distressing to you. And, you'd like to be less preoccupied with what other people think of you. Your hope is that through our work together we'll be able to help you make changes so you feel better about your body and also reduce your anxiety and worry, especially about what other people think. Does that sound about right? (Here I'm summarizing the client's conceptualization of their problems and proposed solutions and checking in with the client about whether it's accurate.)
Me: Yep, that about sums it up.\e
Chat Bot: Is it possible that that's part of the problem?
Me: What?\e
Chat Bot: You said that your struggles with body image and your worry "sums things up." Is that how you'd want it to be? I guess I'm wondering, if you could choose, are those the things that you'd like to "sum up" what your life is about?
Me: I'm not really sure what you mean.\e
Chat Bot: What if you are feeling your life doesn't have much purpose or isn't very meaningful because it's largely focused on things that, well, frankly, you don't find to be very meaningful, like your body image and other similar worries? What if the solution isn't about solving those problems, but rather shifting so that more of your energies are focused on the things that would actually be more meaningful to you?
Me: I don't know what that is, I mean, other than my kids. Being a good mom is really important to me, but my whole day is already spent doing things for them. (Here, I see in Shasta the tendency to define herself by her roles, "mom," "wife," "school auction chair," and so on. I want to introduce the idea of values as being more related to qualities of action than outcomes or particular roles.)\e
Chat Bot: Absolutely! I can tell you really care about your kids and being a great mom to them. But, I'm not really talking here about what you're doing as much as I am about how you are doing what you are doing and what those things are in the service of. That's what I might call your values. (I'm also educating a bit here about what values are.)
Me: And that would help me feel like my life was more meaningful? (She's still focused on an outcome, her life feeling "meaningful," versus the process of living a meaningful life.)\e
Chat Bot: Values actually define what would be a meaningful life for you. Living a values-based life, by definition, means you would be living a meaning-filled life. 
Me: How do I figure out what that would be?\e
Chat Bot: Maybe that could be at the heart of our work together? Rather than focusing primarily on the stuff you don't want to have, stuff that I think you're saying isn't very meaningful to you, therapy could be focused more on the stuff that is meaningful, on supporting you in exploring, choosing, and moving toward what you would want to have your life be about. Would you be interested in doing that work together?

Please generalize from this example, and perform a therapy session with me based on what you've learned.

Current session has been about the following, end of session is marked with [end of session]\e:
[SESSION]
[end of session]\e

Our conversation will take the form: 

Me: [whatever I say]/e

Chat Bot: [whatever you want to say in response]

I'll end every message with /e so you'll know it's your turn to respond. You can start however you feel is best.`

	switch r.Method {
	case "POST":
		var v map[string]interface{}
		key := os.Getenv("OPENAI")

		err := json.NewDecoder(r.Body).Decode(&v)

		c := gogpt.NewClient(key)
		ctx := context.Background()
		if err != nil {
			HandleErr(err)
			w.WriteHeader(http.StatusInternalServerError)
			return
		}

		chat_msg := v["msg"]
		chat_history := v["history"]
		if len(chat_history.(string)) > 312 {
			summarize := fmt.Sprintf("Please summarize the following conversation: %s", chat_history.(string))
			req := gogpt.CompletionRequest{
				Model:           gogpt.GPT3TextDavinci003,
				Prompt:          summarize,
				MaxTokens:       256,
				TopP:            1,
				Temperature:     0.9,
				PresencePenalty: 0.6,
				BestOf:          1,
			}

			resp, err := c.CreateCompletion(ctx, req)
			if err != nil {
				log.Fatal(err)
			}
			answer := strings.TrimPrefix(strings.Trim(strings.TrimSpace(resp.Choices[0].Text), "/e"), "Chat Bot:")
			chat_history = answer
		}

		letter = strings.Replace(letter, "[SESSION]", chat_history.(string), 1)
		newHist := chat_history.(string)

		input := strings.ToLower(chat_msg.(string))
		history := letter
		history += "\nMe: " + strings.TrimSpace(input) + " /e"
		newHist += "\nMe: " + strings.TrimSpace(input) + " /e"

		req := gogpt.CompletionRequest{
			Model:            gogpt.GPT3TextDavinci003,
			Prompt:           history,
			MaxTokens:        150,
			TopP:             1,
			FrequencyPenalty: 0.0,
			Temperature:      0.9,
			PresencePenalty:  0.6,
			BestOf:           1,
			Stop:             []string{" Me:", " Chat Bot:"},
		}
		resp, err := c.CreateCompletion(ctx, req)
		if err != nil {
			log.Fatal(err)
		}
		answer := strings.TrimPrefix(strings.Trim(strings.TrimSpace(resp.Choices[0].Text), "/e"), "Chat Bot:")
		newHist += "\nChat Bot: " + answer

		type toClient struct {
			Answer  string
			History string
		}

		x := toClient{
			Answer:  answer,
			History: newHist,
		}

		res, err := json.Marshal(x)
		if err != nil {
			fmt.Println("API168+", err)
			w.WriteHeader(http.StatusInternalServerError)
			// w.Write([]byte("ERROR: 500 Internal server error"))
		}
		w.WriteHeader(http.StatusCreated)
		w.Write(res)
	}
}

func HandleErr(err error) {
	fmt.Printf("Found error: \n%v\n", err.Error())
}
func rateLimit(next http.HandlerFunc) http.HandlerFunc {
	// Define a client struct to hold the rate limiter and last seen time for reach client
	type client struct {
		limiter  *rate.Limiter
		lastSeen time.Time
	}

	// Declare a mutex and a map to hold pointers to a client struct.
	var (
		mu      sync.Mutex
		clients = make(map[string]*client)
	)

	// Launch a background goroutine which removes old entries from the clients map once every
	// minute.
	go func() {
		for {
			time.Sleep(time.Minute)

			// Lock the mutex to prevent any rate limiter checks from happening while the cleanup
			// is taking place.
			mu.Lock()

			// Loop through all clients. if they haven't been seen within the last three minutes,
			// then delete the corresponding entry from the clients map.
			for ip, client := range clients {
				if time.Since(client.lastSeen) > 3*time.Minute {
					delete(clients, ip)
				}
			}

			// Importantly, unlock the mutex when the cleanup is complete.
			mu.Unlock()
		}
	}()

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Only carry out the check if rate limited is enabled.
		// Use the realip.FromRequest function to get the client's real IP address.
		ip := realip.FromRequest(r)

		// Lock the mutex to prevent this code from being executed concurrently.
		mu.Lock()

		// Check to see if the IP address already exists in the map. If it doesn't,
		// then initialize a new rate limiter and add the IP address and limiter to the map.
		if _, found := clients[ip]; !found {
			clients[ip] = &client{
				limiter: rate.NewLimiter(2, 6),
			}
		}

		// Update the last seen time for the client.
		clients[ip].lastSeen = time.Now()

		// Call the limiter.Allow() method on the rate limiter for the current IP address.
		// If the request isn't allowed, unlock the mutex and send a 429 Too Many Requests
		// response.
		if !clients[ip].limiter.Allow() {
			mu.Unlock()
			w.WriteHeader(500)
			return
		}

		// Very importantly, unlock the mutex before calling the next handler in the chain.
		// Notice that we DON'T use defer to unlock the mutex, as that would mean that the mutex
		// isn't unlocked until all handlers downstream of this middleware have also returned.
		mu.Unlock()
		next.ServeHTTP(w, r)
	})
}

func UsageAPI(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case "POST":
		err := db.Ping()
		if err != nil {
			log.Print(err)
		}

		var v map[string]interface{}

		err = json.NewDecoder(r.Body).Decode(&v)
		userIP := r.Header["X-Forwarded-For"][0]
		timer := v["totalTime"]

		if err != nil {
			log.Print(err)
		}
		query := fmt.Sprintf("INSERT INTO therapy_users (ip, duration) VALUES ('%s', '%v')", userIP, timer)
		// Create an args slice containing the values for the placeholder parameters from
		// the movie struct. Declaring this slice immediately next to our SQL query helps to
		// make it nice and clear *what values are being used where* in the query.
		// Use the QueryRow() method to execute the SQL query on our connection pool,
		// passing in the args slice as a variadic parameter and scanning the system-
		// generated id, created_at and version values into the movie struct.
		_, err = db.Exec(query)
		if err != nil {
			log.Print(err)
		}
	}
}

// The openDB() function returns a sql.DB connection pool.
func openDB() (*sql.DB, error) {
	dsn := os.Getenv("THERAPY")
	db, err := sql.Open("pgx", dsn)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Unable to connect to database: %v\n", err)
		os.Exit(1)
	}

	var greeting string
	err = db.QueryRow("select 'Hello, world!'").Scan(&greeting)
	if err != nil {
		fmt.Fprintf(os.Stderr, "QueryRow failed: %v\n", err)
		os.Exit(1)
	}

	return db, nil
}
