// Package workflow declares conversational workflows. The engine owns task
// execution; providers contribute instructions without importing the engine.
package workflow

import "github.com/xleotranx/wada-kit/kernel"

var Definitions = kernel.NewPoint[Flow]("workflow.definitions")

// Flow supplies the instructions for a main conversation and independent reviews.
// Each phase returns a validated structured result. The engine owns transitions.
type Flow struct {
	ID, Title, Summary                    string
	Plan, Code, Review, Fix, Route, Final string
	MaxReviews                            int
}
