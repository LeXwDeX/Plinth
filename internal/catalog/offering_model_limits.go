package catalog

import (
	"fmt"
	"strings"

	schemacontract "github.com/LeXwDeX/Plinth/internal/schema"
)

// validateOfferingModelLimits checks the two declarations a Processing Plan
// compares before anything is paid (#121): the triangle ceiling a step puts on
// its source model, and how another step's request turns into triangles.
func validateOfferingModelLimits(offering schemacontract.Offering) error {
	if limit := offering.SourceModelLimit; limit != nil {
		if limit.MaxTriangles <= 0 || limit.CountBasis != schemacontract.SourceModelCountBasisTriangles {
			return fmt.Errorf("Offering %q 的 source_model_limit 需要正的 max_triangles 与 count_basis=triangles", offering.ID)
		}
		hasSource := false
		for _, parameter := range offering.Parameters {
			if role, ok := schemacontract.SourceParameterRole(parameter); ok &&
				(role == schemacontract.SemanticSourceTask || role == schemacontract.SemanticSourceAsset) {
				hasSource = true
			}
		}
		if !hasSource {
			return fmt.Errorf("Offering %q 的 source_model_limit 需要 source-task 或 source-asset 参数", offering.ID)
		}
	}
	if estimate := offering.OutputTriangles; estimate != nil {
		if offering.Domain != schemacontract.DomainProcessing {
			return fmt.Errorf("Offering %q 的 output_triangles 只用于处理能力", offering.ID)
		}
		count, countOK := offeringParameter(offering.Parameters, estimate.CountParameter)
		topology, topologyOK := offeringParameter(offering.Parameters, estimate.TopologyParameter)
		if !countOK || count.Type != "number" || !topologyOK || topology.Type != "select" {
			return fmt.Errorf("Offering %q 的 output_triangles 需要 number 类型的 count_parameter 与 select 类型的 topology_parameter", offering.ID)
		}
		if len(estimate.TrianglesPerFace) != len(topology.Options) {
			return fmt.Errorf("Offering %q 的 output_triangles.triangles_per_face 必须覆盖拓扑参数的每个选项且不含多余项", offering.ID)
		}
		for _, option := range topology.Options {
			if factor, declared := estimate.TrianglesPerFace[option.Value]; !declared || factor <= 0 {
				return fmt.Errorf("Offering %q 的 output_triangles.triangles_per_face[%q] 必须是正整数", offering.ID, option.Value)
			}
		}
	}
	if offering.SourceModelLimit != nil || offering.OutputTriangles != nil {
		messages := offering.ExecutionMessages.PlanSourceFaceLimit
		for _, placeholder := range []string{"{source_service}", "{service}", "{estimate}", "{limit}", "{choices}"} {
			if !strings.Contains(messages, placeholder) {
				return fmt.Errorf("Offering %q 声明了来源面数上限或输出面数换算，generation_messages.plan_source_face_limit 必须声明并包含 %s", offering.ID, placeholder)
			}
		}
	}
	return nil
}
