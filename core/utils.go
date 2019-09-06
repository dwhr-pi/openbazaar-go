package core

import (
	"fmt"
	"math/big"
	"strings"
	"time"

	util "gx/ipfs/QmNohiVssaPw3KVLZik59DBVGTSm2dGvYT9eoXt5DQ36Yz/go-ipfs-util"

	"github.com/OpenBazaar/openbazaar-go/pb"
	"github.com/OpenBazaar/openbazaar-go/repo"
	"github.com/OpenBazaar/wallet-interface"
	"github.com/btcsuite/btcd/chaincfg/chainhash"
	"github.com/golang/protobuf/ptypes"
	google_protobuf "github.com/golang/protobuf/ptypes/timestamp"
)

// FormatRFC3339PB returns the given `google_protobuf.Timestamp` as a RFC3339
// formatted string
func FormatRFC3339PB(ts google_protobuf.Timestamp) string {
	return util.FormatRFC3339(time.Unix(ts.Seconds, int64(ts.Nanos)).UTC())
}

// BuildTransactionRecords - Used by the GET order API to build transaction records suitable to be included in the order response
func (n *OpenBazaarNode) BuildTransactionRecords(contract *pb.RicardianContract, records []*wallet.TransactionRecord, state pb.OrderState) ([]*pb.TransactionRecord, *pb.TransactionRecord, error) {
	paymentRecords := []*pb.TransactionRecord{}
	payments := make(map[string]*pb.TransactionRecord)
	wal, err := n.Multiwallet.WalletForCurrencyCode(contract.BuyerOrder.Payment.AmountValue.Currency.Code)
	if err != nil {
		return paymentRecords, nil, err
	}

	// Consolidate any transactions with multiple outputs into a single record
	for _, r := range records {
		record, ok := payments[r.Txid]
		if ok {
			n, _ := new(big.Int).SetString(record.TxnValue.Amount, 10)
			sum := new(big.Int).Add(n, &r.Value)
			record.TxnValue = &pb.CurrencyValue{
				Currency: record.TxnValue.Currency,
				Amount:   sum.String(),
			}
			payments[r.Txid] = record
		} else {
			tx := new(pb.TransactionRecord)
			tx.Txid = r.Txid
			tx.TxnValue = &pb.CurrencyValue{
				Currency: contract.BuyerOrder.Payment.AmountValue.Currency,
				Amount:   r.Value.String(),
			} // r.Value
			ts, err := ptypes.TimestampProto(r.Timestamp)
			if err != nil {
				return paymentRecords, nil, err
			}
			tx.Timestamp = ts
			ch, err := chainhash.NewHashFromStr(strings.TrimPrefix(tx.Txid, "0x"))
			if err != nil {
				return paymentRecords, nil, err
			}
			confirmations, height, err := wal.GetConfirmations(*ch)
			if err != nil {
				return paymentRecords, nil, err
			}
			tx.Height = uint64(height)
			tx.Confirmations = uint64(confirmations)
			payments[r.Txid] = tx
		}
	}
	for _, rec := range payments {
		paymentRecords = append(paymentRecords, rec)
	}
	var refundRecord *pb.TransactionRecord
	if contract != nil && (state == pb.OrderState_REFUNDED || state == pb.OrderState_DECLINED || state == pb.OrderState_CANCELED) && contract.BuyerOrder != nil && contract.BuyerOrder.Payment != nil {
		// For multisig we can use the outgoing from the payment address
		if contract.BuyerOrder.Payment.Method == pb.Order_Payment_MODERATED || state == pb.OrderState_DECLINED || state == pb.OrderState_CANCELED {
			for _, rec := range payments {
				val, _ := new(big.Int).SetString(rec.TxnValue.Amount, 10)
				if val.Cmp(big.NewInt(0)) < 0 {
					refundRecord = new(pb.TransactionRecord)
					refundRecord.Txid = rec.Txid
					refundRecord.TxnValue = &pb.CurrencyValue{
						Currency: rec.TxnValue.Currency,
						Amount:   "-" + rec.TxnValue.Amount,
					} //-rec.Value
					refundRecord.Confirmations = rec.Confirmations
					refundRecord.Height = rec.Height
					refundRecord.Timestamp = rec.Timestamp
					break
				}
			}
		} else if contract.Refund != nil && contract.Refund.RefundTransaction != nil && contract.Refund.Timestamp != nil {
			refundRecord = new(pb.TransactionRecord)
			// Direct we need to use the transaction info in the contract's refund object
			ch, err := chainhash.NewHashFromStr(strings.TrimPrefix(contract.Refund.RefundTransaction.Txid, "0x"))
			if err != nil {
				return paymentRecords, refundRecord, err
			}
			confirmations, height, err := wal.GetConfirmations(*ch)
			if err != nil {
				return paymentRecords, refundRecord, nil
			}
			refundRecord.Txid = contract.Refund.RefundTransaction.Txid
			refundRecord.TxnValue = contract.Refund.RefundTransaction.NewValue
			refundRecord.Timestamp = contract.Refund.Timestamp
			refundRecord.Confirmations = uint64(confirmations)
			refundRecord.Height = uint64(height)
		}
	}
	return paymentRecords, refundRecord, nil
}

// NormalizeCurrencyCode standardizes the format for the given currency code
func (n *OpenBazaarNode) NormalizeCurrencyCode(currencyCode string) string {
	var c, err = repo.LoadCurrencyDefinitions().Lookup(currencyCode)
	if err != nil {
		log.Errorf("invalid currency code (%s): %s", currencyCode, err.Error())
		return ""
	}
	if n.TestnetEnable {
		return c.TestnetString()
	}
	return c.String()
}

func (n *OpenBazaarNode) ValidateMultiwalletHasPreferredCurrencies(data repo.SettingsData) error {
	if data.PreferredCurrencies == nil {
		return nil
	}
	for _, cc := range *data.PreferredCurrencies {
		_, err := n.Multiwallet.WalletForCurrencyCode(cc)
		if err != nil {
			return fmt.Errorf("preferred coin %s not found in multiwallet", cc)
		}
	}
	return nil
}
