// Package wireless holds the evidence values shared by every wireless
// package: observations, snapshots, coverage and projections of collected
// controller facts, their wire JSON encoding (MarshalJSON keeps integer and
// float intent) and the configuration Fingerprint that apply compares.
//
// Generic Record maps are the parser output; the typed controller model in
// package wlan is decoded from them strictly.
package wireless
