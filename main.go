package main

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"log"
	"os"
	"os/user"
	"strings"
	"time"

	"github.com/chromedp/chromedp"

	markdown "github.com/MichaelMure/go-term-markdown"
)

const ctxTime = 2000
const maxCopyRetries = 30

// filteredErrorf suppresses noisy CDP events that chromedp doesn't handle yet.
func filteredErrorf(format string, v ...interface{}) {
	msg := fmt.Sprintf(format, v...)
	if strings.Contains(msg, "EventTopLayerElementsUpdated") {
		return
	}
	log.Printf(format, v...)
}

// a list of all possible common executable names
// for chromium-based browsers.
var browsers = []string{
	"chromium",
	"chromium-browser",
	"google-chrome",
	"google-chrome-stable",
	"microsoft-edge",
	"microsoft-edge-stable",
	"brave-browser",
	"vivaldi",
	"opera",
	"msedge",
	"ungoogled-chromium",
}

func detectBrowser() (string, error) {
	var basePaths = []string{
		"/bin/",
		"/usr/bin/",
	}
	for _, basePath := range basePaths {
		for _, name := range browsers {
			path := basePath + name
			if _, err := os.Stat(path); err == nil {
				fmt.Println(path)
				return path, nil
			}
		}
	}
	return "", fmt.Errorf("no Chromium-based browser found in PATH")
}

func main() {
	usr, err := user.Current()
	if err != nil {
		fmt.Println("Error fetching user info:", err)
		return
	}

	configDir := usr.HomeDir + "/.config/chatbang"
	configPath := configDir + "/chatbang"
	profileDir := usr.HomeDir + "/.config/chatbang/profile_data"

	err = os.MkdirAll(configDir, 0o755)
	if err != nil {
		fmt.Println("Error creating config directory:", err)
		return
	}

	configFile, err := os.OpenFile(configPath,
		os.O_RDWR|os.O_CREATE, 0o644)
	if err != nil {
		fmt.Println("Error opening config file:", err)
		return
	}
	defer configFile.Close()

	info, err := configFile.Stat()
	if err != nil {
		fmt.Println("Error getting file info:", err)
		return
	}

	if info.Size() == 0 {
		configFile.Seek(0, 0)
	}

	// read browser from config
	var defaultBrowser string
	var projectSlug string
	scanner := bufio.NewScanner(configFile)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		parts := strings.SplitN(line, "=", 2)
		if len(parts) == 2 {
			key := strings.TrimSpace(parts[0])
			val := strings.TrimSpace(parts[1])
			if key == "browser" {
				defaultBrowser = val
			} else if key == "project" {
				projectSlug = val
			}
		}
	}

	// Step 2: if config is empty or invalid, detect in PATH
	if defaultBrowser == "" {
		detectedBrowser, err := detectBrowser()
		if err != nil {
			fmt.Println("No Chromium-based browser found in PATH or config.")
			fmt.Println("Please install a Chromium-based browser or edit the config at", configPath)
			return
		}

		defaultBrowser = detectedBrowser
		defaultbrowserConfig := "browser=" + defaultBrowser

		_, err = io.WriteString(configFile, defaultbrowserConfig)
		if err != nil {
			fmt.Println("Error writing default config:", err)
			return
		}
	}

	if len(os.Args) > 1 {
		if os.Args[1] == "--config" {
			loginProfile(defaultBrowser, profileDir)
			return
		}

		if os.Args[1] == "--help" || os.Args[1] == "-h" {
			helpStr := "`Chatbang` is a simple tool to access ChatGPT from the terminal, without needing for an API key.  \n"

			helpStr += "## Configuration  \n `Chatbang` requires a Chromium-based browser (e.g. Chrome, Edge, Brave) to work, so you need to have one. And then make sure that it points to the right path to your chosen browser in the default config path for `Chatbang`: `$HOME/.config/chatbang/chatbang`.  \n\nIt's default is: ``` browser=/usr/bin/google-chrome ```  \nChange it to your favorite Chromium-based browser.  \n\n"

			helpStr += "You also need to log in to ChatGPT in `Chatbang`'s Chromium session, so you need to do: ```bash chatbang --config ``` That will open `Chatbang`'s Chromium session on ChatGPT's website, log in with your account. Then, you will need to allow the clipboard permission for ChatGPT's website (on the same session).  \n\n"

			res := markdown.Render(string(helpStr), 80, 2)
			fmt.Println(string(res))
			return
		}
	}

	fmt.Print("> ") // first prompt

	allocatorCtx, cancel := chromedp.NewExecAllocator(context.Background(),
		append(chromedp.DefaultExecAllocatorOptions[:],
			chromedp.ExecPath(defaultBrowser),
			chromedp.Flag("disable-blink-features", "AutomationControlled"),
			chromedp.Flag("exclude-switches", "enable-automation"),
			chromedp.Flag("disable-extensions", false),
			chromedp.UserAgent("Mozilla/5.0 (X11; Linux x86_64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/120.0.0.0 Safari/537.36"),
			chromedp.Flag("disable-default-apps", false),
			chromedp.Flag("disable-dev-shm-usage", false),
			chromedp.Flag("disable-gpu", false),
			//chromedp.Flag("headless", false),
			chromedp.UserDataDir(profileDir),
			chromedp.Flag("profile-directory", "Default"),
		)...,
	)

	defer cancel()

	ctx, cancel := chromedp.NewContext(allocatorCtx,
		chromedp.WithErrorf(filteredErrorf),
	)
	defer cancel()

	taskCtx, taskCancel := context.WithTimeout(ctx, ctxTime*time.Second)
	defer taskCancel()

	// Build the navigation URL. If a project is configured, use its URL.
	chatURL := "https://chatgpt.com"
	if projectSlug != "" {
		if strings.HasPrefix(projectSlug, "http") {
			chatURL = projectSlug
		} else {
			chatURL = "https://chatgpt.com/" + projectSlug
		}
	}

	err = chromedp.Run(taskCtx,
		chromedp.Navigate(chatURL),
	)

	if err != nil {
		log.Fatal(err)
	}

	promptScanner := bufio.NewScanner(os.Stdin)

	for promptScanner.Scan() {
		firstPrompt := promptScanner.Text()
		if len(firstPrompt) > 0 {
			runChatGPT(taskCtx, defaultBrowser, profileDir, firstPrompt)
			return
		}

		fmt.Print("> ")
	}
}

func runChatGPT(taskCtx context.Context, browserPath string, profileDir string, firstPrompt string) {
	fmt.Printf("[Thinking...]\n\n")

	buttonDiv := `button[data-testid="copy-turn-action-button"]`

	modifiedPrompt := firstPrompt
	var responseText string

	// Intercept clipboard writes AND copy events so we can capture the
	// markdown without needing the transient clipboard-read permission.
	hookJS := `(() => {
		window.__cb_text = '';
		const orig = navigator.clipboard.writeText.bind(navigator.clipboard);
		navigator.clipboard.writeText = (text) => {
			window.__cb_text = text;
			return orig(text);
		};
		document.addEventListener('copy', (e) => {
			const data = e.clipboardData && e.clipboardData.getData('text/plain');
			if (data) window.__cb_text = data;
		});
	})()`

	// Read the intercepted text. DOM fallback skips thinking blocks.
	readJS := `(() => {
		if (window.__cb_text && window.__cb_text.length > 0) {
			return window.__cb_text;
		}
		// Fallback: extract from last assistant message, excluding thinking
		let msgs = document.querySelectorAll('[data-message-author-role="assistant"]');
		for (let i = msgs.length - 1; i >= 0; i--) {
			let clone = msgs[i].cloneNode(true);
			// Remove thinking / reasoning blocks
			clone.querySelectorAll('[data-testid="message-thinking"], .thinking, [class*="thinking"]').forEach(e => e.remove());
			let text = clone.innerText && clone.innerText.trim();
			if (text && text.length > 10) return text;
		}
		return '';
	})()`

	err := chromedp.Run(taskCtx,
		chromedp.WaitVisible(`#prompt-textarea`, chromedp.ByID),
		chromedp.Click(`#prompt-textarea`, chromedp.ByID),
		chromedp.SendKeys(`#prompt-textarea`, modifiedPrompt, chromedp.ByID),
		chromedp.Click(`#composer-submit-button`, chromedp.ByID),
		chromedp.Click(`#prompt-textarea`, chromedp.ByID),
	)

	for retries := 0; retries < maxCopyRetries; retries++ {
		if responseText != "" && responseText != modifiedPrompt {
			break
		}
		err = chromedp.Run(taskCtx,
			chromedp.Sleep(4*time.Second),
			chromedp.WaitVisible(buttonDiv, chromedp.ByQuery),

			// Inject the clipboard hook
			chromedp.Evaluate(hookJS, nil),

			// Click the last copy button (triggers writeText)
			chromedp.Evaluate(fmt.Sprintf(`
					(() => {
					    let buttons = document.querySelectorAll('%s');
					    if (buttons.length > 0) {
						buttons[buttons.length - 1].click();
					    }
					})()
				    `, buttonDiv), nil),

			chromedp.Sleep(500*time.Millisecond),
			// Read the intercepted text (with DOM fallback)
			chromedp.Evaluate(readJS, &responseText),
		)
		if err != nil {
			break
		}

		// ChatGPT streams: copy button appears early. Re-capture after
		// a delay to get the complete response.
		if responseText != "" && responseText != modifiedPrompt {
			chromedp.Run(taskCtx,
				chromedp.Sleep(2*time.Second),
				chromedp.Evaluate(fmt.Sprintf(`
						(() => {
						    let buttons = document.querySelectorAll('%s');
						    if (buttons.length > 0) {
							buttons[buttons.length - 1].click();
						    }
						})()
					    `, buttonDiv), nil),
				chromedp.Sleep(500*time.Millisecond),
				chromedp.Evaluate(readJS, &responseText),
			)
		}
	}

	if err != nil {
		log.Fatal(err)
	}

	if responseText == "" || responseText == modifiedPrompt {
		log.Fatal("Failed to get a response from ChatGPT — the page structure may have changed.")
	}

	fmt.Fprintf(os.Stderr, "\n[DEBUG captured %d chars]: %q\n\n", len(responseText), responseText[:min(len(responseText), 200)])

	result := markdown.Render(responseText, 80, 2)
	fmt.Println(string(result))

	fmt.Print("> ")
	promptScanner := bufio.NewScanner(os.Stdin)
	for promptScanner.Scan() {
		prompt := promptScanner.Text()
		modifiedPrompt = prompt
		if len(prompt) == 0 {
			fmt.Print("> ")
			continue
		}

		fmt.Printf("[Thinking...]\n\n")

		err := chromedp.Run(taskCtx,
			chromedp.WaitVisible(`#prompt-textarea`, chromedp.ByID),
			chromedp.Click(`#prompt-textarea`, chromedp.ByID),
			chromedp.SendKeys(`#prompt-textarea`, modifiedPrompt, chromedp.ByID),
			chromedp.Click(`#composer-submit-button`, chromedp.ByID),
			chromedp.Click(`#prompt-textarea`, chromedp.ByID),
		)

		if err != nil {
			log.Fatal(err)
		}

		responseText := ""

		for retries := 0; retries < maxCopyRetries; retries++ {
			if responseText != "" && responseText != modifiedPrompt {
				break
			}

			err = chromedp.Run(taskCtx,
				chromedp.Sleep(4*time.Second),

				// Inject the clipboard hook
				chromedp.Evaluate(hookJS, nil),

				// Click the last copy button (triggers writeText)
				chromedp.Evaluate(fmt.Sprintf(`
						(() => {
						    let buttons = document.querySelectorAll('%s');
						    if (buttons.length > 0) {
							buttons[buttons.length - 1].click();
						    }
						})()
					    `, buttonDiv), nil),

				chromedp.Sleep(1*time.Second),
				// Read the intercepted text (with DOM fallback)
				chromedp.Evaluate(readJS, &responseText),
			)
			if err != nil {
				break
			}

			// Re-capture after a delay for complete response.
			if responseText != "" && responseText != modifiedPrompt {
				chromedp.Run(taskCtx,
					chromedp.Sleep(2*time.Second),
					chromedp.Evaluate(fmt.Sprintf(`
								(() => {
								    let buttons = document.querySelectorAll('%s');
								    if (buttons.length > 0) {
									buttons[buttons.length - 1].click();
								    }
								})()
							    `, buttonDiv), nil),
					chromedp.Sleep(500*time.Millisecond),
					chromedp.Evaluate(readJS, &responseText),
				)
			}
		}

		if responseText == "" || responseText == modifiedPrompt {
			log.Fatal("Failed to get a response from ChatGPT — the page structure may have changed.")
		}

		fmt.Fprintf(os.Stderr, "\n[DEBUG captured %d chars]: %q\n\n", len(responseText), responseText[:min(len(responseText), 200)])

		result := markdown.Render(responseText, 80, 2)
		fmt.Println(string(result))

		fmt.Print("> ")
	}
}

func loginProfile(defaultBrowser string, profileDir string) {
	browserPath := defaultBrowser

	allocatorCtx, cancel := chromedp.NewExecAllocator(context.Background(),
		append(chromedp.DefaultExecAllocatorOptions[:],
			chromedp.ExecPath(browserPath),
			chromedp.Flag("disable-blink-features", "AutomationControlled"),
			chromedp.Flag("exclude-switches", "enable-automation"),
			chromedp.Flag("disable-extensions", false),
			chromedp.UserAgent("Mozilla/5.0 (X11; Linux x86_64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/120.0.0.0 Safari/537.36"),
			chromedp.Flag("disable-default-apps", false),
			chromedp.Flag("disable-dev-shm-usage", false),
			chromedp.Flag("disable-gpu", false),
			chromedp.Flag("headless", false),
			chromedp.UserDataDir(profileDir),
			chromedp.Flag("profile-directory", "Default"),
		)...,
	)

	defer cancel()

	ctx, cancel := chromedp.NewContext(allocatorCtx,
		chromedp.WithErrorf(filteredErrorf),
	)
	defer cancel()

	taskCtx, taskCancel := context.WithTimeout(ctx, ctxTime*time.Second)
	defer taskCancel()

	err := chromedp.Run(taskCtx,
		chromedp.Navigate(`https://www.chatgpt.com/`),
		chromedp.Evaluate(`(async () => {
			const permName = 'clipboard-read';
			try {
				const p = await navigator.permissions.query({ name: permName });
				if (p.state !== 'granted') {
					alert("Please allow clipboard access in the popup that will appear now.");
				}
			} catch (e) {
				try {
					await navigator.clipboard.readText();
				} catch (_) {
					alert("Please allow clipboard access in the popup that will appear now.");
				}
			}
		})();`, nil),
		chromedp.Evaluate(`navigator.clipboard.readText().catch(() => {});`, nil),
	)

	if err != nil {
		log.Fatal(err)
	}

	done := make(chan bool)
	go func() {
		ticker := time.NewTicker(ctxTime * time.Second)
		defer ticker.Stop()

		for {
			select {
			case <-ticker.C:
				err := chromedp.Run(ctx, chromedp.Evaluate(`document.readyState`, nil))
				if err != nil {
					done <- true
					return
				}
			case <-ctx.Done():
				done <- true
				return
			}
		}
	}()

	<-done
}
