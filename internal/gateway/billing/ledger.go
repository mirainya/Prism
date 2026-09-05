package billing

import "fmt"

type Direction string

const (
	Debit  Direction = "debit"
	Credit Direction = "credit"
)

type LedgerEntry struct {
	Code      string
	Direction Direction
	Amount    Amount
}

// ValidateBalanced enforces a single-currency, positive, balanced journal
// before it is handed to the database posting procedure.
func ValidateBalanced(entries []LedgerEntry) error {
	if len(entries) < 2 {
		return fmt.Errorf("ledger requires at least two entries")
	}
	debit, credit := Zero(), Zero()
	seen := make(map[string]struct{}, len(entries))
	for _, entry := range entries {
		if entry.Code == "" || entry.Amount.Sign() <= 0 {
			return fmt.Errorf("ledger entry %q must have a positive amount", entry.Code)
		}
		if _, exists := seen[entry.Code]; exists {
			return fmt.Errorf("duplicate ledger entry code %q", entry.Code)
		}
		seen[entry.Code] = struct{}{}
		switch entry.Direction {
		case Debit:
			debit = debit.Add(entry.Amount)
		case Credit:
			credit = credit.Add(entry.Amount)
		default:
			return fmt.Errorf("ledger entry %q has invalid direction %q", entry.Code, entry.Direction)
		}
	}
	if debit.Cmp(credit) != 0 {
		return fmt.Errorf("ledger is not balanced: debit=%s credit=%s", debit, credit)
	}
	return nil
}
