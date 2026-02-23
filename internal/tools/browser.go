package tools

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/go-rod/rod"
	"github.com/go-rod/rod/lib/launcher"
	"github.com/go-rod/rod/lib/proto"

	"github.com/openclio/openclio/internal/config"
)

const browserTextMaxChars = 50_000

// BrowserTool provides a safe subset of browser automation via Chrome CDP.
type BrowserTool struct {
	cfg config.BrowserToolConfig

	mu      sync.Mutex
	browser *rod.Browser
	page    *rod.Page
}

func NewBrowserTool(cfg config.BrowserToolConfig) *BrowserTool {
	return &BrowserTool{cfg: cfg}
}

func (t *BrowserTool) Name() string { return "browser" }
func (t *BrowserTool) Description() string {
	return "Control a real browser with JavaScript execution: browse pages, extract rendered text, click, fill forms, submit, and capture screenshots. Use this for dynamic sites where web_fetch returns incomplete HTML."
}
func (t *BrowserTool) Schema() json.RawMessage {
	return json.RawMessage(`{
		"type": "object",
		"properties": {
			"action": {
				"type": "string",
				"enum": ["browse", "navigate", "get_text", "click", "fill", "submit", "screenshot"],
				"description": "Browser action to perform"
			},
			"url": {"type": "string", "description": "Target URL for navigate"},
			"selector": {"type": "string", "description": "CSS selector for click/fill/submit"},
			"text": {"type": "string", "description": "Visible text match for click"},
			"value": {"type": "string", "description": "Value to input for fill"}
		},
		"required": ["action"]
	}`)
}

type browserParams struct {
	Action   string `json:"action"`
	URL      string `json:"url,omitempty"`
	Selector string `json:"selector,omitempty"`
	Text     string `json:"text,omitempty"`
	Value    string `json:"value,omitempty"`
}

func (t *BrowserTool) Execute(ctx context.Context, params json.RawMessage) (string, error) {
	var p browserParams
	if err := json.Unmarshal(params, &p); err != nil {
		return "", fmt.Errorf("invalid params: %w", err)
	}
	action := strings.ToLower(strings.TrimSpace(p.Action))
	if action == "" {
		return "", fmt.Errorf("action is required")
	}

	t.mu.Lock()
	defer t.mu.Unlock()

	page, err := t.ensurePage(ctx)
	if err != nil {
		return "", err
	}
	timeout := t.cfg.Timeout
	if timeout <= 0 {
		timeout = 30 * time.Second
	}
	page = page.Timeout(timeout)

	switch action {
	case "browse":
		url := strings.TrimSpace(p.URL)
		if url == "" {
			return "", fmt.Errorf("url is required for browse")
		}
		if !strings.HasPrefix(url, "http://") && !strings.HasPrefix(url, "https://") {
			url = "https://" + url
		}
		if err := page.Navigate(url); err != nil {
			return "", fmt.Errorf("navigate failed: %w", err)
		}
		_ = page.WaitLoad()
		time.Sleep(1200 * time.Millisecond)
		text, err := t.extractPageText(page)
		if err != nil {
			return "", err
		}
		return text, nil

	case "navigate":
		url := strings.TrimSpace(p.URL)
		if url == "" {
			return "", fmt.Errorf("url is required for navigate")
		}
		if !strings.HasPrefix(url, "http://") && !strings.HasPrefix(url, "https://") {
			url = "https://" + url
		}
		if err := page.Navigate(url); err != nil {
			return "", fmt.Errorf("navigate failed: %w", err)
		}
		_ = page.WaitLoad()
		return "Navigated to " + url, nil

	case "get_text":
		return t.extractPageText(page)

	case "click":
		el, err := t.findElement(page, p.Selector, p.Text)
		if err != nil {
			return "", err
		}
		if err := el.Click(proto.InputMouseButtonLeft, 1); err != nil {
			return "", fmt.Errorf("click failed: %w", err)
		}
		_ = page.WaitLoad()
		return "Click completed.", nil

	case "fill":
		selector := strings.TrimSpace(p.Selector)
		if selector == "" {
			return "", fmt.Errorf("selector is required for fill")
		}
		el, err := page.Element(selector)
		if err != nil {
			return "", fmt.Errorf("field not found: %w", err)
		}
		if err := el.Input(p.Value); err != nil {
			return "", fmt.Errorf("fill failed: %w", err)
		}
		return "Field updated.", nil

	case "submit":
		selector := strings.TrimSpace(p.Selector)
		if selector != "" {
			el, err := page.Element(selector)
			if err != nil {
				return "", fmt.Errorf("submit target not found: %w", err)
			}
			_, err = el.Eval(`(el) => {
				if (!el) return "missing";
				if (el.tagName === "FORM" && typeof el.submit === "function") {
					el.submit();
					return "ok";
				}
				if (el.form && typeof el.form.submit === "function") {
					el.form.submit();
					return "ok";
				}
				if (typeof el.click === "function") {
					el.click();
					return "clicked";
				}
				return "unsupported";
			}`)
			if err != nil {
				return "", fmt.Errorf("submit failed: %w", err)
			}
			_ = page.WaitLoad()
			return "Form submitted.", nil
		}
		if _, err := page.Eval(`() => {
			const form = document.querySelector("form");
			if (!form) return "no-form";
			form.submit();
			return "ok";
		}`); err != nil {
			return "", fmt.Errorf("submit failed: %w", err)
		}
		_ = page.WaitLoad()
		return "Form submitted.", nil

	case "screenshot":
		shot, err := page.Screenshot(true, &proto.PageCaptureScreenshot{})
		if err != nil {
			return "", fmt.Errorf("screenshot failed: %w", err)
		}
		return base64.StdEncoding.EncodeToString(shot), nil

	default:
		return "", fmt.Errorf("unsupported action %q", action)
	}
}

func (t *BrowserTool) extractPageText(page *rod.Page) (string, error) {
	el, err := page.Element("body")
	if err != nil {
		return "", fmt.Errorf("cannot read page body: %w", err)
	}
	text, err := el.Text()
	if err != nil {
		return "", fmt.Errorf("extracting page text failed: %w", err)
	}
	text = strings.TrimSpace(text)
	if len(text) > browserTextMaxChars {
		text = text[:browserTextMaxChars] + "\n...[truncated]"
	}
	if text == "" {
		return "[empty page text]", nil
	}
	return text, nil
}

func (t *BrowserTool) ensurePage(ctx context.Context) (*rod.Page, error) {
	if t.page != nil && t.browser != nil {
		return t.page, nil
	}
	if err := t.ensureBrowser(ctx); err != nil {
		return nil, err
	}
	page, err := t.browser.Page(proto.TargetCreateTarget{URL: "about:blank"})
	if err != nil {
		return nil, fmt.Errorf("creating browser page failed: %w", err)
	}
	t.page = page
	return page, nil
}

func (t *BrowserTool) ensureBrowser(ctx context.Context) error {
	if t.browser != nil {
		return nil
	}
	launch := launcher.New().Leakless(false).Headless(t.cfg.Headless)
	if strings.TrimSpace(t.cfg.ChromePath) != "" {
		launch = launch.Bin(t.cfg.ChromePath)
	}
	url, err := launch.Launch()
	if err != nil {
		return fmt.Errorf("launching chrome failed: %w", err)
	}

	browser := rod.New().ControlURL(url)
	if err := browser.Connect(); err != nil {
		return fmt.Errorf("connecting to browser failed: %w", err)
	}
	select {
	case <-ctx.Done():
		_ = browser.Close()
		return ctx.Err()
	default:
	}
	t.browser = browser
	return nil
}

func (t *BrowserTool) findElement(page *rod.Page, selector, text string) (*rod.Element, error) {
	selector = strings.TrimSpace(selector)
	text = strings.TrimSpace(text)
	if selector != "" {
		el, err := page.Element(selector)
		if err != nil {
			return nil, fmt.Errorf("element not found for selector %q: %w", selector, err)
		}
		return el, nil
	}
	if text == "" {
		return nil, fmt.Errorf("either selector or text is required")
	}

	quoted := regexp.QuoteMeta(text)
	el, err := page.ElementR("a,button,input[type='submit'],input[type='button'],[role='button']", quoted)
	if err != nil {
		return nil, fmt.Errorf("click target with text %q not found: %w", text, err)
	}
	return el, nil
}
