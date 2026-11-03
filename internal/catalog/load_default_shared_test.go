package catalog

import (
	"bytes"
	"encoding/json"
	"reflect"
	"sort"
	"sync"
	"testing"

	schemacontract "github.com/LeXwDeX/Plinth/internal/schema"
)

// LoadDefault hands every caller the same *Catalog. Whatever a caller does to
// the values the read methods return must not reach that shared instance:
// scribble over every reachable field of every returned value, with readers on
// other goroutines, then compare against a freshly parsed catalog.
func TestLoadDefaultSharedInstanceIsNotChangedThroughReturnedValues(t *testing.T) {
	shared, err := LoadDefault()
	if err != nil {
		t.Fatal(err)
	}
	again, err := LoadDefault()
	if err != nil {
		t.Fatal(err)
	}
	if shared != again {
		t.Fatal("LoadDefault should return the one shared instance")
	}
	fresh, err := Load(bytes.NewReader(defaultCatalog))
	if err != nil {
		t.Fatal(err)
	}
	ids := make([]schemacontract.OfferingID, 0, len(shared.byID))
	for id := range shared.byID {
		ids = append(ids, id)
	}
	sort.Slice(ids, func(i, j int) bool { return ids[i] < ids[j] })
	if len(ids) == 0 {
		t.Fatal("default catalog has no offerings")
	}

	if !reflect.DeepEqual(shared.Application(), cloneApplication(shared.application)) {
		t.Fatal("Application() should match cloneApplication of the stored schema")
	}

	stop := make(chan struct{})
	var readers sync.WaitGroup
	for range 4 {
		readers.Add(1)
		go func() {
			defer readers.Done()
			for {
				select {
				case <-stop:
					return
				default:
				}
				readEverything(shared, ids)
			}
		}()
	}

	application := shared.Application()
	scribble(reflect.ValueOf(&application).Elem(), 0)
	for _, id := range ids {
		offering, _ := shared.Get(id)
		scribble(reflect.ValueOf(&offering).Elem(), 0)
	}
	list := shared.List("")
	scribble(reflect.ValueOf(&list).Elem(), 0)
	for _, offering := range fresh.List("") {
		normalized, input, err := shared.Normalize(offering.Capability, offering.ID, json.RawMessage(`{}`))
		if err != nil {
			continue
		}
		scribble(reflect.ValueOf(&normalized).Elem(), 0)
		scribble(reflect.ValueOf(&input).Elem(), 0)
	}
	cancellation := shared.DefaultCancellationPolicy()
	scribble(reflect.ValueOf(&cancellation).Elem(), 0)
	successors := shared.RetiredModelSuccessors()
	scribble(reflect.ValueOf(&successors).Elem(), 0)
	for _, connection := range fresh.application.Runtime.ProviderConnections {
		policy, _ := shared.ResultRefetchPolicy(connection.AdapterKey)
		scribble(reflect.ValueOf(&policy).Elem(), 0)
	}
	close(stop)
	readers.Wait()

	if !reflect.DeepEqual(shared.Application(), fresh.Application()) {
		t.Fatal("Application() of the shared catalog changed after callers edited returned values")
	}
	for _, id := range ids {
		got, gotOK := shared.Get(id)
		want, wantOK := fresh.Get(id)
		if gotOK != wantOK || !reflect.DeepEqual(got, want) {
			t.Fatalf("Get(%q) of the shared catalog changed after callers edited returned values", id)
		}
	}
	if !reflect.DeepEqual(shared.List(""), fresh.List("")) {
		t.Fatal("List of the shared catalog changed after callers edited returned values")
	}
	if !reflect.DeepEqual(shared.DefaultCancellationPolicy(), fresh.DefaultCancellationPolicy()) {
		t.Fatal("DefaultCancellationPolicy of the shared catalog changed")
	}
	if !reflect.DeepEqual(shared.RetiredModelSuccessors(), fresh.RetiredModelSuccessors()) {
		t.Fatal("RetiredModelSuccessors of the shared catalog changed")
	}
}

func readEverything(c *Catalog, ids []schemacontract.OfferingID) {
	_ = c.Application()
	for _, id := range ids {
		_, _ = c.Get(id)
	}
	_ = c.List("")
	_ = c.DefaultCancellationPolicy()
	_ = c.RetiredModelSuccessors()
}

// scribble overwrites every settable value reachable from v: strings, numbers
// and booleans change, slices and maps are walked element by element, pointers
// and interfaces are followed. Unexported fields are skipped.
func scribble(v reflect.Value, depth int) {
	if depth > 64 || !v.IsValid() {
		return
	}
	switch v.Kind() {
	case reflect.Pointer:
		if !v.IsNil() {
			scribble(v.Elem(), depth+1)
		}
	case reflect.Interface:
		if v.IsNil() {
			return
		}
		inner := reflect.New(v.Elem().Type()).Elem()
		inner.Set(v.Elem())
		scribble(inner, depth+1)
		if v.CanSet() {
			v.Set(inner)
		}
	case reflect.Struct:
		for i := range v.NumField() {
			if field := v.Field(i); field.CanSet() {
				scribble(field, depth+1)
			}
		}
	case reflect.Slice, reflect.Array:
		for i := range v.Len() {
			scribble(v.Index(i), depth+1)
		}
	case reflect.Map:
		if v.IsNil() {
			return
		}
		for _, key := range v.MapKeys() {
			value := reflect.New(v.Type().Elem()).Elem()
			value.Set(v.MapIndex(key))
			scribble(value, depth+1)
			v.SetMapIndex(key, value)
		}
	case reflect.String:
		if v.CanSet() {
			v.SetString(v.String() + "~")
		}
	case reflect.Bool:
		if v.CanSet() {
			v.SetBool(!v.Bool())
		}
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		if v.CanSet() {
			v.SetInt(v.Int() + 1)
		}
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		if v.CanSet() {
			v.SetUint(v.Uint() + 1)
		}
	case reflect.Float32, reflect.Float64:
		if v.CanSet() {
			v.SetFloat(v.Float() + 1)
		}
	}
}
