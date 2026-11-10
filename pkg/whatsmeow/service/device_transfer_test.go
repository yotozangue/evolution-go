package whatsmeow_service

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"

	"go.mau.fi/whatsmeow/util/keys"
)

func chavesDeTeste() *DeviceKeys {
	noise, identity, spk := keys.NewKeyPair(), keys.NewKeyPair(), keys.NewKeyPair()
	return &DeviceKeys{
		Jid:             "5511999998888:5@s.whatsapp.net",
		Lid:             "115522490376340:5@lid",
		PushName:        "Loja",
		Platform:        "android",
		RegistrationID:  21,
		NoiseKey:        noise.Priv[:],
		IdentityKey:     identity.Priv[:],
		SignedPreKey:    spk.Priv[:],
		SignedPreKeyID:  1,
		SignedPreKeySig: bytes.Repeat([]byte{7}, 64),
		AdvKey:          bytes.Repeat([]byte{1}, 32),
		AdvDetails:      []byte{1, 2, 3},
		AdvAccountSig:   bytes.Repeat([]byte{2}, 64),
		AdvAccountKey:   bytes.Repeat([]byte{3}, 32),
		AdvDeviceSig:    bytes.Repeat([]byte{4}, 64),
	}
}

func TestToDeviceMontaOAparelhoComAsChavesPublicasDerivadas(t *testing.T) {
	d := chavesDeTeste()

	device, err := d.toDevice()
	if err != nil {
		t.Fatal(err)
	}

	if device.ID.String() != d.Jid || device.LID.String() != d.Lid {
		t.Fatalf("jid/lid: %s %s", device.ID, device.LID)
	}
	esperado := keys.NewKeyPairFromPrivateKey([32]byte(d.IdentityKey))
	if *device.IdentityKey.Pub != *esperado.Pub {
		t.Fatal("chave pública da identidade não foi derivada da privada")
	}
	if device.SignedPreKey.KeyID != 1 || !bytes.Equal(device.SignedPreKey.Signature[:], d.SignedPreKeySig) {
		t.Fatal("signed pre-key")
	}
	if !bytes.Equal(device.Account.DeviceSignature, d.AdvDeviceSig) || device.RegistrationID != 21 {
		t.Fatal("dados de vínculo")
	}
}

func TestToDeviceRecusaDadosIncompletos(t *testing.T) {
	casos := map[string]func(*DeviceKeys){
		"jid sem device":   func(d *DeviceKeys) { d.Jid = "5511999998888@s.whatsapp.net" },
		"jid de grupo":     func(d *DeviceKeys) { d.Jid = "120363000000000000@g.us" },
		"noise_key curta":  func(d *DeviceKeys) { d.NoiseKey = d.NoiseKey[:31] },
		"assinatura curta": func(d *DeviceKeys) { d.SignedPreKeySig = d.SignedPreKeySig[:63] },
		"sem device sig":   func(d *DeviceKeys) { d.AdvDeviceSig = nil },
		"adv_key sem 32 b": func(d *DeviceKeys) { d.AdvKey = d.AdvKey[:16] },
	}
	for nome, estraga := range casos {
		d := chavesDeTeste()
		estraga(d)
		if _, err := d.toDevice(); err == nil {
			t.Errorf("%s: devia recusar", nome)
		}
	}
}

// O JSON é o mesmo que o exportador do Evolution API gera: bytes em base64.
func TestDeviceKeysLeOJsonDoExportador(t *testing.T) {
	var d DeviceKeys
	j := `{"jid":"5511999998888:5@s.whatsapp.net","noise_key":"AQID","pre_keys":{"7":"BAUG"},"first_unuploaded_pre_key_id":813}`
	if err := json.NewDecoder(strings.NewReader(j)).Decode(&d); err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(d.NoiseKey, []byte{1, 2, 3}) || !bytes.Equal(d.PreKeys["7"], []byte{4, 5, 6}) || d.FirstUnuploadedPreKeyID != 813 {
		t.Fatalf("decodificação: %+v", d)
	}
}
