package db

import (
	"testing"

	"github.com/jackc/pgx/v5/pgtype"
)

func TestSystemParameterStruct(t *testing.T) {
	param := SystemParameter{
		ID:          pgtype.UUID{Bytes: [16]byte{1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11, 12, 13, 14, 15, 16}, Valid: true},
		Category:    "country",
		KeyName:     "ID",
		Value:       "Indonesia",
		Description: pgtype.Text{String: "Oona Indonesia Entity", Valid: true},
		IsActive:    true,
	}

	if param.Category != "country" {
		t.Errorf("expected category country, got %s", param.Category)
	}
	if param.KeyName != "ID" {
		t.Errorf("expected key_name ID, got %s", param.KeyName)
	}
	if param.Value != "Indonesia" {
		t.Errorf("expected value Indonesia, got %s", param.Value)
	}
	if !param.IsActive {
		t.Errorf("expected is_active true, got false")
	}
}

func TestSystemParameterParams(t *testing.T) {
	createArg := CreateSystemParameterParams{
		Category:    "product",
		KeyName:     "Health",
		Value:       "Health Insurance",
		Description: pgtype.Text{String: "Health Insurance Product", Valid: true},
		IsActive:    true,
	}

	if createArg.Category != "product" || createArg.KeyName != "Health" {
		t.Errorf("CreateSystemParameterParams structure invalid")
	}

	updateArg := UpdateSystemParameterParams{
		Category:    "product",
		KeyName:     "Health",
		Value:       "Updated Health Insurance",
		Description: pgtype.Text{String: "Updated Description", Valid: true},
		IsActive:    true,
	}

	if updateArg.Value != "Updated Health Insurance" {
		t.Errorf("UpdateSystemParameterParams structure invalid")
	}
}
