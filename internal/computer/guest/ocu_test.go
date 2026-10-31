package guest

import (
	"encoding/json"
	"strings"
	"testing"
)

func ocuCall(tool, args string) ocuCallArgs {
	return ocuCallArgs{Tool: tool, Arguments: json.RawMessage(args)}
}

func TestDesktopRelayRefusesToolsAndArgumentsOutsideTheAllowlist(t *testing.T) {
	for name, in := range map[string]ocuCallArgs{
		"drag":               ocuCall("drag", `{"app":"pcmanfm","from_x":1,"from_y":1,"to_x":2,"to_y":2}`),
		"unknown tool":       ocuCall("doctor", `{}`),
		"coordinate click":   ocuCall("click", `{"app":"pcmanfm","x":10,"y":10}`),
		"click method":       ocuCall("click", `{"app":"pcmanfm","element_index":"3","click_method":"global"}`),
		"window placement":   ocuCall("get_app_state", `{"app":"pcmanfm","window_placement":"agent_display"}`),
		"key method":         ocuCall("press_key", `{"app":"pcmanfm","key":"Return","key_method":"sky_key"}`),
		"extra on list":      ocuCall("list_apps", `{"app":"x"}`),
		"not an object":      ocuCall("get_app_state", `[1]`),
		"missing app":        ocuCall("get_app_state", `{}`),
		"empty app":          ocuCall("get_app_state", `{"app":"  "}`),
		"missing element":    ocuCall("click", `{"app":"pcmanfm"}`),
		"missing value":      ocuCall("set_value", `{"app":"a","element_index":"2"}`),
		"missing action":     ocuCall("perform_secondary_action", `{"app":"a","element_index":"2"}`),
		"negative index":     ocuCall("click", `{"app":"a","element_index":-1}`),
		"huge tree":          ocuCall("get_app_state", `{"app":"a","max_tree_nodes":100000}`),
		"text for the limit": ocuCall("get_app_state", `{"app":"a","text_limit":"all"}`),
		"empty keys":         ocuCall("press_key", `{"app":"a","key":""}`),
		"long text":          ocuCall("type_text", `{"app":"a","text":"`+strings.Repeat("x", ocuTextMax+1)+`"}`),
		"nul in app":         ocuCall("get_app_state", `{"app":"a\u0000b"}`),
	} {
		if _, err := checkOCUCall(in); err == nil {
			t.Errorf("%s was relayed", name)
		}
	}
	for name, in := range map[string]ocuCallArgs{
		"list":      ocuCall("list_apps", `{}`),
		"list null": ocuCall("list_apps", `null`),
		"state":     ocuCall("get_app_state", `{"app":"pcmanfm","max_tree_depth":20,"text_limit":"max"}`),
		"click":     ocuCall("click", `{"app":"pcmanfm","element_index":"4","mouse_button":"right"}`),
		"clear":     ocuCall("set_value", `{"app":"a","element_index":"2","value":""}`),
		"scroll":    ocuCall("scroll", `{"app":"a","element_index":"2","direction":"down","pages":2}`),
		"type":      ocuCall("type_text", `{"app":"a","text":"hello"}`),
		"key":       ocuCall("press_key", `{"app":"a","key":"ctrl+l"}`),
		"secondary": ocuCall("perform_secondary_action", `{"app":"a","element_index":"2","action":"expand"}`),
	} {
		if _, err := checkOCUCall(in); err != nil {
			t.Errorf("%s was refused: %v", name, err)
		}
	}
	args, err := checkOCUCall(ocuCall("click", `{"app":"pcmanfm","element_index":7}`))
	if err != nil || args["element_index"] != "7" {
		t.Fatalf("a numeric index must reach the engine as text: %v %v", args, err)
	}
}

func TestDesktopResultKeepsTextAndCountsImages(t *testing.T) {
	raw := json.RawMessage(`{"isError":false,"content":[{"type":"text","text":"App=pcmanfm\n0 window"},{"type":"image","data":"AAAA","mimeType":"image/png"}]}`)
	res, err := ocuResult("get_app_state", raw, nil)
	if err != nil {
		t.Fatal(err)
	}
	if res["text"] != "App=pcmanfm\n0 window" || res["images_omitted"] != 1 || res["is_error"] != false {
		t.Fatalf("unexpected result: %v", res)
	}
	long := json.RawMessage(`{"content":[{"type":"text","text":"` + strings.Repeat("a", ocuResultCap+10) + `"}]}`)
	res, _ = ocuResult("get_app_state", long, nil)
	if res["truncated"] != true || !strings.Contains(res["text"].(string), "result cut") {
		t.Fatal("a long tree must be cut with a note")
	}
}
