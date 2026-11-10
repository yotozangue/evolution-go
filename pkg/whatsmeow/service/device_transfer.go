package whatsmeow_service

import (
	"context"
	"fmt"
	"strconv"

	"go.mau.fi/whatsmeow/proto/waAdv"
	"go.mau.fi/whatsmeow/store"
	"go.mau.fi/whatsmeow/types"
	"go.mau.fi/whatsmeow/util/keys"
)

// DeviceKeys é o aparelho vinculado (companion) com as chaves que o WhatsApp
// conhece. Serve para trazer um número de outro cliente (ex.: Evolution API /
// Baileys) sem ler QR de novo, e para levá-lo de volta. []byte vai em base64
// no JSON.
type DeviceKeys struct {
	Jid             string `json:"jid"`
	Lid             string `json:"lid"`
	PushName        string `json:"push_name"`
	Platform        string `json:"platform"`
	RegistrationID  uint32 `json:"registration_id"`
	NoiseKey        []byte `json:"noise_key"`
	IdentityKey     []byte `json:"identity_key"`
	SignedPreKey    []byte `json:"signed_pre_key"`
	SignedPreKeyID  uint32 `json:"signed_pre_key_id"`
	SignedPreKeySig []byte `json:"signed_pre_key_sig"`
	AdvKey          []byte `json:"adv_key"`
	AdvDetails      []byte `json:"adv_details"`
	AdvAccountSig   []byte `json:"adv_account_sig"`
	AdvAccountKey   []byte `json:"adv_account_sig_key"`
	AdvDeviceSig    []byte `json:"adv_device_sig"`
	// Pre-keys com id menor que este já foram enviadas ao servidor do WhatsApp.
	FirstUnuploadedPreKeyID uint32            `json:"first_unuploaded_pre_key_id"`
	PreKeys                 map[string][]byte `json:"pre_keys"`
}

func chave32(nome string, b []byte) (*[32]byte, error) {
	if len(b) != 32 {
		return nil, fmt.Errorf("%s deve ter 32 bytes, veio %d", nome, len(b))
	}
	var k [32]byte
	copy(k[:], b)
	return &k, nil
}

func (d *DeviceKeys) toDevice() (*store.Device, error) {
	jid, err := types.ParseJID(d.Jid)
	if err != nil || jid.Server != types.DefaultUserServer || jid.Device == 0 {
		return nil, fmt.Errorf("jid inválido para aparelho vinculado: %q", d.Jid)
	}
	var lid types.JID
	if d.Lid != "" {
		if lid, err = types.ParseJID(d.Lid); err != nil {
			return nil, fmt.Errorf("lid inválido: %q", d.Lid)
		}
	}
	noise, err := chave32("noise_key", d.NoiseKey)
	if err != nil {
		return nil, err
	}
	identity, err := chave32("identity_key", d.IdentityKey)
	if err != nil {
		return nil, err
	}
	spk, err := chave32("signed_pre_key", d.SignedPreKey)
	if err != nil {
		return nil, err
	}
	if len(d.SignedPreKeySig) != 64 {
		return nil, fmt.Errorf("signed_pre_key_sig deve ter 64 bytes, veio %d", len(d.SignedPreKeySig))
	}
	var sig [64]byte
	copy(sig[:], d.SignedPreKeySig)
	if len(d.AdvKey) != 32 || len(d.AdvDetails) == 0 || len(d.AdvAccountSig) == 0 ||
		len(d.AdvAccountKey) == 0 || len(d.AdvDeviceSig) == 0 {
		return nil, fmt.Errorf("dados de vínculo (adv_*) incompletos")
	}

	return &store.Device{
		NoiseKey:       keys.NewKeyPairFromPrivateKey(*noise),
		IdentityKey:    keys.NewKeyPairFromPrivateKey(*identity),
		SignedPreKey:   &keys.PreKey{KeyPair: *keys.NewKeyPairFromPrivateKey(*spk), KeyID: d.SignedPreKeyID, Signature: &sig},
		RegistrationID: d.RegistrationID,
		AdvSecretKey:   d.AdvKey,
		ID:             &jid,
		LID:            lid,
		Account: &waAdv.ADVSignedDeviceIdentity{
			Details:             d.AdvDetails,
			AccountSignatureKey: d.AdvAccountKey,
			AccountSignature:    d.AdvAccountSig,
			DeviceSignature:     d.AdvDeviceSig,
		},
		Platform: d.Platform,
		PushName: d.PushName,
	}, nil
}

// stopClient derruba o websocket da instância e a tira do runtime, sem logout:
// o aparelho continua vinculado ao número.
func (w whatsmeowService) stopClient(instanceId string) {
	if client, exists := w.clientPointer[instanceId]; exists {
		if client.IsConnected() {
			client.Disconnect()
		}
		if mycli, ok := w.myClientPointer[instanceId]; ok && mycli.eventHandlerID != 0 {
			client.RemoveEventHandler(mycli.eventHandlerID)
		}
	}
	if killChan, exists := w.killChannel[instanceId]; exists {
		select {
		case killChan <- true:
		default:
		}
	}
	delete(w.clientPointer, instanceId)
	delete(w.myClientPointer, instanceId)
	delete(w.killChannel, instanceId)
	if instance, err := w.instanceRepository.GetInstanceByID(instanceId); err == nil {
		w.userInfoCache.Delete(instance.Token)
	}
}

// ImportDevice grava o aparelho no store do whatsmeow, aponta a instância para
// ele e sobe o cliente já logado.
func (w whatsmeowService) ImportDevice(instanceId string, d *DeviceKeys) error {
	device, err := d.toDevice()
	if err != nil {
		return err
	}
	instance, err := w.instanceRepository.GetInstanceByID(instanceId)
	if err != nil {
		return err
	}
	container, err := w.getSharedContainer()
	if err != nil {
		return err
	}
	ctx := context.Background()

	w.stopClient(instanceId)

	// PutDevice não sobrescreve as chaves de um jid que já existe; apagar antes
	// leva junto (cascade) pre-keys, sessões e identidades antigas.
	if err := container.DeleteDevice(ctx, &store.Device{ID: device.ID}); err != nil {
		return fmt.Errorf("apagando aparelho anterior: %w", err)
	}
	if err := container.PutDevice(ctx, device); err != nil {
		return fmt.Errorf("gravando aparelho: %w", err)
	}
	for id, priv := range d.PreKeys {
		keyID, err := strconv.ParseUint(id, 10, 32)
		if err != nil {
			return fmt.Errorf("id de pre-key inválido: %q", id)
		}
		if len(priv) != 32 {
			return fmt.Errorf("pre-key %s deve ter 32 bytes", id)
		}
		_, err = sharedAuthDB.ExecContext(ctx,
			`INSERT INTO whatsmeow_pre_keys (jid, key_id, key, uploaded) VALUES ($1, $2, $3, $4)
			 ON CONFLICT (jid, key_id) DO UPDATE SET key=excluded.key, uploaded=excluded.uploaded`,
			device.ID.String(), keyID, priv, uint32(keyID) < d.FirstUnuploadedPreKeyID)
		if err != nil {
			return fmt.Errorf("gravando pre-key %s: %w", id, err)
		}
	}

	if err := w.instanceRepository.UpdateJid(instance.Id, device.ID.String()); err != nil {
		return err
	}
	w.loggerWrapper.GetLogger(instanceId).LogInfo("[%s] Aparelho %s importado com %d pre-keys", instanceId, device.ID, len(d.PreKeys))
	return w.StartInstance(instanceId)
}

// ExportDevice devolve o aparelho da instância no mesmo formato do import.
func (w whatsmeowService) ExportDevice(instanceId string) (*DeviceKeys, error) {
	instance, err := w.instanceRepository.GetInstanceByID(instanceId)
	if err != nil {
		return nil, err
	}
	if instance.Jid == "" {
		return nil, fmt.Errorf("instância sem aparelho vinculado")
	}
	jid, err := types.ParseJID(instance.Jid)
	if err != nil {
		return nil, err
	}
	container, err := w.getSharedContainer()
	if err != nil {
		return nil, err
	}
	ctx := context.Background()
	device, err := container.GetDevice(ctx, jid)
	if err != nil {
		return nil, err
	}
	if device == nil {
		return nil, fmt.Errorf("aparelho %s não está no store", jid)
	}

	d := &DeviceKeys{
		Jid:             device.ID.String(),
		PushName:        device.PushName,
		Platform:        device.Platform,
		RegistrationID:  device.RegistrationID,
		NoiseKey:        device.NoiseKey.Priv[:],
		IdentityKey:     device.IdentityKey.Priv[:],
		SignedPreKey:    device.SignedPreKey.Priv[:],
		SignedPreKeyID:  device.SignedPreKey.KeyID,
		SignedPreKeySig: device.SignedPreKey.Signature[:],
		AdvKey:          device.AdvSecretKey,
		AdvDetails:      device.Account.Details,
		AdvAccountSig:   device.Account.AccountSignature,
		AdvAccountKey:   device.Account.AccountSignatureKey,
		AdvDeviceSig:    device.Account.DeviceSignature,
		PreKeys:         map[string][]byte{},
	}
	if !device.LID.IsEmpty() {
		d.Lid = device.LID.String()
	}

	rows, err := sharedAuthDB.QueryContext(ctx, `SELECT key_id, key, uploaded FROM whatsmeow_pre_keys WHERE jid=$1`, d.Jid)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var keyID uint32
		var key []byte
		var uploaded bool
		if err := rows.Scan(&keyID, &key, &uploaded); err != nil {
			return nil, err
		}
		d.PreKeys[strconv.FormatUint(uint64(keyID), 10)] = key
		if uploaded && keyID+1 > d.FirstUnuploadedPreKeyID {
			d.FirstUnuploadedPreKeyID = keyID + 1
		}
	}
	return d, rows.Err()
}

// ReleaseDevice tira o aparelho do GO sem logout, para outro cliente assumir:
// derruba o cliente, apaga o store e desvincula a instância.
func (w whatsmeowService) ReleaseDevice(instanceId string) error {
	instance, err := w.instanceRepository.GetInstanceByID(instanceId)
	if err != nil {
		return err
	}
	w.stopClient(instanceId)
	if instance.Jid != "" {
		jid, err := types.ParseJID(instance.Jid)
		if err != nil {
			return err
		}
		container, err := w.getSharedContainer()
		if err != nil {
			return err
		}
		if err := container.DeleteDevice(context.Background(), &store.Device{ID: &jid}); err != nil {
			return err
		}
	}
	if err := w.instanceRepository.UpdateJid(instance.Id, ""); err != nil {
		return err
	}
	return w.instanceRepository.UpdateConnected(instance.Id, false, "Released")
}
