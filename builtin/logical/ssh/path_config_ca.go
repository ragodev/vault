package ssh

import (
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/pem"
	"fmt"

	"github.com/hashicorp/vault/logical"
	"github.com/hashicorp/vault/logical/framework"
	"golang.org/x/crypto/ssh"
)

func pathConfigCA(b *backend) *framework.Path {
	return &framework.Path{
		Pattern: "config/ca",
		Fields: map[string]*framework.FieldSchema{
			"private_key": &framework.FieldSchema{
				Type:        framework.TypeString,
				Description: `Private half of the SSH key that will be used to sign certificates.`,
			},
			"public_key": &framework.FieldSchema{
				Type:        framework.TypeString,
				Description: `Public half of the SSH key that will be used to sign certificates.`,
			},
			"generate_signing_key": &framework.FieldSchema{
				Type:        framework.TypeBool,
				Description: `Generate SSH key pair internally rather than use the private_key and public_key fields.`,
				Default:     true,
			},
		},

		Callbacks: map[logical.Operation]framework.OperationFunc{
			logical.UpdateOperation: b.pathCAWrite,
		},

		HelpSynopsis: `Set the SSH private key used for signing certificates.`,
		HelpDescription: `This sets the CA information used for certificates generated by this
by this mount. The fields must be in the standard private and public SSH format.

For security reasons, the private key cannot be retrieved later.`,
	}
}

func (b *backend) pathCAWrite(req *logical.Request, data *framework.FieldData) (*logical.Response, error) {
	var err error
	publicKey := data.Get("public_key").(string)
	privateKey := data.Get("private_key").(string)

	var generateSigningKey bool

	generateSigningKeyRaw, ok := data.GetOk("generate_signing_key")
	switch {
	// explicitly set true
	case ok && generateSigningKeyRaw.(bool):
		if publicKey != "" || privateKey != "" {
			return logical.ErrorResponse("public_key and private_key must not be set when generate_signing_key is set to true"), nil
		}

		generateSigningKey = true

		// explicitly set to false, or not set and we have both a public and private key
	case ok, publicKey != "" && privateKey != "":
		if publicKey == "" {
			return logical.ErrorResponse("missing public_key"), nil
		}

		if privateKey == "" {
			return logical.ErrorResponse("missing private_key"), nil
		}

		_, err := ssh.ParsePrivateKey([]byte(privateKey))
		if err != nil {
			return logical.ErrorResponse(fmt.Sprintf("Unable to parse private_key as an SSH private key: %v", err)), nil
		}

		_, err = parsePublicSSHKey(publicKey)
		if err != nil {
			return logical.ErrorResponse(fmt.Sprintf("Unable to parse public_key as an SSH public key: %v", err)), nil
		}

		// not set and no public/private key provided so generate
	case publicKey == "" && privateKey == "":
		publicKey, privateKey, err = generateSSHKeyPair()
		if err != nil {
			return nil, err
		}

		generateSigningKey = true

	default: // not set, but one or the other supplied
		return logical.ErrorResponse("only one of public_key and private_key set; both must be set to use, or both must be blank to auto-generate"), nil
	}

	if generateSigningKey {
		publicKey, privateKey, err = generateSSHKeyPair()
		if err != nil {
			return nil, err
		}
	}

	if publicKey == "" || privateKey == "" {
		return nil, fmt.Errorf("failed to generate or parse the keys")
	}

	err = req.Storage.Put(&logical.StorageEntry{
		Key:   "public_key",
		Value: []byte(publicKey),
	})
	if err != nil {
		return nil, err
	}

	bundle := signingBundle{
		Certificate: privateKey,
	}

	entry, err := logical.StorageEntryJSON("config/ca_bundle", bundle)
	if err != nil {
		return nil, err
	}

	err = req.Storage.Put(entry)
	return nil, err
}

func generateSSHKeyPair() (string, string, error) {
	privateSeed, err := rsa.GenerateKey(rand.Reader, 4096)
	if err != nil {
		return "", "", err
	}

	privateBlock := &pem.Block{
		Type:    "RSA PRIVATE KEY",
		Headers: nil,
		Bytes:   x509.MarshalPKCS1PrivateKey(privateSeed),
	}

	public, err := ssh.NewPublicKey(&privateSeed.PublicKey)
	if err != nil {
		return "", "", err
	}

	return string(ssh.MarshalAuthorizedKey(public)), string(pem.EncodeToMemory(privateBlock)), nil
}