package guest

import (
	"os/exec"
	"strings"
	"testing"
)

func TestPrivateBrowserInputBounds(t *testing.T) {
	for _, origin := range []string{"http://example.test", "https://example.test/", "https://example.test?q=1", "https://example.test#x", "https://user:pass@example.test", "https://example.test?", ""} {
		if _, err := privateBrowserInputExpression(privateBrowserInput{Origin: origin, Text: "fake"}); err == nil {
			t.Fatalf("invalid origin accepted: %s", origin)
		}
	}
	for _, text := range []string{"a\x00b", string([]byte{0xff}), strings.Repeat("x", MaxDesktopInput+1)} {
		if _, err := privateBrowserInputExpression(privateBrowserInput{Origin: "https://example.test", Text: text}); err == nil {
			t.Fatal("invalid private input accepted")
		}
	}
	for _, origin := range []string{"https://example.test", "https://127.0.0.1:1234", "https://[::1]:1234"} {
		if _, err := privateBrowserInputExpression(privateBrowserInput{Origin: origin, Text: "quoted \" \\ \n 中文"}); err != nil {
			t.Fatal(err)
		}
	}
	if !desktopNeedsSession("browser.type_private") || !needsSharedInputGate("browser.type_private") || viewerDesktopAction("browser.type_private") {
		t.Fatal("private input must use the shared mutation gate and desktop lifecycle")
	}
}

func TestPrivateBrowserInputFieldGuard(t *testing.T) {
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("Node is required to execute the page-operation contract")
	}
	// Fake DOM validates the actual production function; real Chrome coverage is
	// opt-in. Synthetic text is deliberately not a JavaScript-safe string literal.
	expression, err := privateBrowserInputExpression(privateBrowserInput{Origin: "https://example.test", Text: "fake \" \\ \n 中文"})
	if err != nil {
		t.Fatal(err)
	}
	script := `const assert = require('node:assert/strict');
class Input {
  constructor() { this.type='password'; this.isConnected=true; this.events=[]; this.stored=''; }
  set value(v) { this.stored=v; }
  getBoundingClientRect() { return {width:100,height:30,top:20,left:20,bottom:50,right:120}; }
  dispatchEvent(e) { this.events.push(e.type); }
}
global.HTMLInputElement=Input;
global.Event=class { constructor(type) { this.type=type; } };
global.innerWidth=800; global.innerHeight=600;
let visible=true;
global.getComputedStyle=()=>({visibility:visible?'visible':'hidden',display:'block',opacity:'1'});
global.location={origin:'https://example.test'};
global.document={hasFocus:()=>true,visibilityState:'visible'};
const apply=()=>` + expression + `;
for (const scenario of ['text','disabled','readonly','detached','hidden','offscreen','unfocused','background','origin','iframe']) {
  const field=new Input(); document.activeElement=field;
  document.hasFocus=()=>scenario!=='unfocused'; document.visibilityState=scenario==='background'?'hidden':'visible';
  location.origin=scenario==='origin'?'https://other.test':'https://example.test'; visible=scenario!=='hidden';
  if(scenario==='text') field.type='text';
  if(scenario==='disabled') field.disabled=true;
  if(scenario==='readonly') field.readOnly=true;
  if(scenario==='detached') field.isConnected=false;
  if(scenario==='offscreen') field.getBoundingClientRect=()=>({width:100,height:30,top:900,left:20,bottom:930,right:120});
  if(scenario==='iframe') document.activeElement={};
  assert.equal(apply(),false,scenario); assert.equal(field.stored,'',scenario); assert.deepEqual(field.events,[],scenario);
}
for (const shadow of [false,true]) {
  const field=new Input(); document.activeElement=shadow?{shadowRoot:{activeElement:field}}:field;
  document.hasFocus=()=>true; document.visibilityState='visible'; location.origin='https://example.test'; visible=true;
  assert.equal(apply(),true); assert.equal(field.stored,'fake " \\ \n 中文'); assert.deepEqual(field.events,['input','change']);
}`
	cmd := exec.Command(node)
	cmd.Stdin = strings.NewReader(script)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("page-operation contract failed: %v: %s", err, out)
	}
}
