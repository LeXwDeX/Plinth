// Command fake-upstream serves the fake upstream APIs of
// internal/testutil/fakeupstream on loopback, so a Plinth stack in live mode
// can run against Tripo and an OpenAI-compatible LLM gateway at zero cost
// (issue #96). Test tooling only: it is not built into any image.
//
//	fake-upstream -addr 127.0.0.1:19000
//
// Point the stack at <origin>/tripo/v3 (TRIPO_BASE_URL) and <origin>/openai/v1
// (LLM_BASE_URL). Any API key is accepted; a key or an X-Fake-Scenario header
// selects a fault (see -help).
package main

import (
	"bufio"
	"context"
	"errors"
	"flag"
	"fmt"
	"log"
	"net"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"time"

	"github.com/LeXwDeX/Plinth/internal/testutil/fakeupstream"
	"github.com/LeXwDeX/Plinth/internal/testutil/fakeupstream/registry"
)

func main() {
	if err := run(os.Args[1:]); err != nil {
		fmt.Fprintln(os.Stderr, "fake-upstream:", err)
		os.Exit(1)
	}
}

func run(args []string) error {
	flags := flag.NewFlagSet("fake-upstream", flag.ContinueOnError)
	flags.Usage = func() {
		fmt.Fprintf(flags.Output(), "Usage of fake-upstream:\n")
		flags.PrintDefaults()
		fmt.Fprintf(flags.Output(), "\nFault scenarios (X-Fake-Scenario header, or an API key starting with %q+name):\n  %s\n",
			fakeupstream.KeyPrefix, strings.Join(fakeupstream.SortedScenarioNames(), "\n  "))
	}
	addr := flags.String("addr", "127.0.0.1:0", "listen address; non-loopback addresses are refused unless -allow-non-loopback")
	allow := flags.Bool("allow-non-loopback", false, "allow listening on a non-loopback address")
	queued := flags.Int("queued-polls", 1, "task polls that answer queued")
	running := flags.Int("running-polls", 1, "task polls that answer running")
	failTimes := flags.Int("fail-times", 0, "how many requests a request-phase scenario fails before answering normally (0 = always)")
	artifactOrigin := flags.String("artifact-origin", "", "origin written into artifact URLs (default: the origin the client used)")
	verbose := flags.Bool("log", true, "log one line per request (method, path, scenario, status; never the key or bodies)")
	hold := flags.Duration("timeout-hold", 2*time.Minute, "how long the timeout scenario holds a request")
	if err := flags.Parse(args); err != nil {
		return err
	}
	listener, err := net.Listen("tcp", *addr)
	if err != nil {
		return err
	}
	defer listener.Close()
	if tcp, ok := listener.Addr().(*net.TCPAddr); ok && !tcp.IP.IsLoopback() && !*allow {
		return fmt.Errorf("refusing to listen on non-loopback %s", tcp)
	}
	server, err := registry.New(fakeupstream.Config{QueuedPolls: *queued, RunningPolls: *running, FailTimes: *failTimes, TimeoutHold: *hold, ArtifactOrigin: *artifactOrigin})
	if err != nil {
		return err
	}
	origin := "http://" + listener.Addr().String()
	for _, version := range server.Versions() {
		fmt.Printf("%s/%s  %s%s\n", version.Provider, version.API, origin, version.Prefix)
	}
	fmt.Printf("listening %s\n", origin)
	var handler http.Handler = server
	if *verbose {
		handler = logRequests(server)
	}
	httpServer := &http.Server{Handler: handler, ReadHeaderTimeout: 10 * time.Second}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()
	go func() {
		<-ctx.Done()
		shutdown, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = httpServer.Shutdown(shutdown)
	}()
	if err := httpServer.Serve(listener); err != nil && !errors.Is(err, http.ErrServerClosed) {
		log.Print(err)
		return err
	}
	return nil
}

type statusWriter struct {
	http.ResponseWriter
	status int
}

func (w *statusWriter) WriteHeader(status int) {
	w.status = status
	w.ResponseWriter.WriteHeader(status)
}

func (w *statusWriter) Flush() {
	if flusher, ok := w.ResponseWriter.(http.Flusher); ok {
		flusher.Flush()
	}
}

// Hijack keeps the lost-response and timeout scenarios working under logging.
func (w *statusWriter) Hijack() (net.Conn, *bufio.ReadWriter, error) {
	return w.ResponseWriter.(http.Hijacker).Hijack()
}

func logRequests(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		wrapped := &statusWriter{ResponseWriter: w, status: http.StatusOK}
		defer func() {
			recovered := recover()
			scenario := fakeupstream.ScenarioOf(r)
			log.Printf("%s %s scenario=%q status=%d", r.Method, r.URL.Path, scenario, wrapped.status)
			if recovered != nil {
				panic(recovered)
			}
		}()
		next.ServeHTTP(wrapped, r)
	})
}
