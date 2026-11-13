package httpapi

import (
	"encoding/json"
	"testing"

	"github.com/LeXwDeX/Plinth/internal/catalog"
	"github.com/LeXwDeX/Plinth/internal/generation"
)

// #284: a succeeded Task that did not deliver everything carries notices
// [{code, detail, inputs}]. The detail is the whole Schema sentence in the
// request locale, naming the positions the notice is about; the state, assets
// and failure are untouched.
func TestTaskResponseCarriesTheNoticesOfASucceededTask(t *testing.T) {
	t.Parallel()
	product, err := catalog.LoadDefault()
	if err != nil {
		t.Fatal(err)
	}
	application := product.Application()
	texts, err := catalog.NewTextLocalizer(application)
	if err != nil {
		t.Fatal(err)
	}
	s := &Server{generation: &generation.Module{}, application: application, texts: texts}
	type notice struct {
		Code   string `json:"code"`
		Detail string `json:"detail"`
		Inputs []int  `json:"inputs"`
	}
	partial := []generation.TaskNotice{{Code: generation.NoticePartialInputs, Inputs: []int{2, 4}}}
	for _, test := range []struct {
		name   string
		locale string
		task   generation.Task
		want   []notice
	}{
		{"names the pictures", "", generation.Task{State: generation.TaskSucceeded, Notices: partial},
			[]notice{{Code: "partial_inputs", Detail: "第 2 张、第 4 张没有处理成功，其余已完成。", Inputs: []int{2, 4}}}},
		{"names the pictures in English", "en", generation.Task{State: generation.TaskSucceeded, Notices: partial},
			[]notice{{Code: "partial_inputs", Detail: "Not processed: picture 2, picture 4. The rest are done.", Inputs: []int{2, 4}}}},
		{"a position that cannot be named", "", generation.Task{State: generation.TaskSucceeded,
			Notices: []generation.TaskNotice{{Code: generation.NoticePartialInputs}}},
			[]notice{{Code: "partial_inputs", Detail: "部分图片没有处理成功，其余已完成。"}}},
		{"a code the Schema has no sentence for is left out", "", generation.Task{State: generation.TaskSucceeded,
			Notices: []generation.TaskNotice{{Code: "no_such_notice", Inputs: []int{1}}, {Code: generation.NoticePartialInputs, Inputs: []int{3}}}},
			[]notice{{Code: "partial_inputs", Detail: "第 3 张没有处理成功，其余已完成。", Inputs: []int{3}}}},
		{"a Task that delivered everything", "", generation.Task{State: generation.TaskSucceeded}, nil},
		{"notices belong to a success only", "", generation.Task{State: generation.TaskMaterializing, Notices: partial}, nil},
		{"a failed Task says none", "", generation.Task{State: generation.TaskFailed, Notices: partial}, nil},
	} {
		t.Run(test.name, func(t *testing.T) {
			test.task.ID = "task"
			response := s.newTaskResponse(test.locale, test.task)
			encoded, err := json.Marshal(response)
			if err != nil {
				t.Fatal(err)
			}
			var decoded struct {
				State   string   `json:"state"`
				Notices []notice `json:"notices"`
			}
			if err := json.Unmarshal(encoded, &decoded); err != nil {
				t.Fatal(err)
			}
			got, _ := json.Marshal(decoded.Notices)
			want, _ := json.Marshal(test.want)
			if string(got) != string(want) {
				t.Fatalf("notices = %s, want %s (response %s)", got, want, encoded)
			}
			if decoded.State != string(test.task.State) {
				t.Fatalf("a notice changed the state: %s", encoded)
			}
		})
	}
}

// A Task with no notice leaves the key out of the response entirely.
func TestTaskResponseWithoutNoticesOmitsTheKey(t *testing.T) {
	t.Parallel()
	product, err := catalog.LoadDefault()
	if err != nil {
		t.Fatal(err)
	}
	s := &Server{generation: &generation.Module{}, application: product.Application()}
	encoded, err := json.Marshal(s.newTaskResponse("", generation.Task{ID: "task", State: generation.TaskSucceeded}))
	if err != nil {
		t.Fatal(err)
	}
	var decoded map[string]any
	if err := json.Unmarshal(encoded, &decoded); err != nil {
		t.Fatal(err)
	}
	if _, present := decoded["notices"]; present {
		t.Fatalf("notices present without a notice: %s", encoded)
	}
}
