// Package visor pkg/visor/rpc_mail.go c3-vis-core
package visor

import (
	"github.com/skycoin/skywire/pkg/cipher"
	"github.com/skycoin/skywire/pkg/skymail"
	"github.com/skycoin/skywire/pkg/util/rpcutil"
	"github.com/skycoin/skywire/pkg/visor/visorapi"
)

// MailStatus reports the mailbox state. See Visor.MailStatus.
func (r *RPC) MailStatus(_ *struct{}, out *visorapi.MailStatus) (err error) {
	defer rpcutil.LogCall(r.log, "MailStatus", nil)(out, &err)
	st, err := r.visor.MailStatus()
	if err != nil {
		return err
	}
	*out = *st
	return nil
}

// MailList lists a folder. See Visor.MailList.
func (r *RPC) MailList(folder *string, out *[]skymail.Summary) (err error) {
	defer rpcutil.LogCall(r.log, "MailList", folder)(nil, &err)
	*out, err = r.visor.MailList(*folder)
	return err
}

// MailRead renders one message. See Visor.MailRead.
func (r *RPC) MailRead(in *visorapi.MailMessageRequest, out *skymail.Rendered) (err error) {
	defer rpcutil.LogCall(r.log, "MailRead", in)(nil, &err)
	m, err := r.visor.MailRead(in.Folder, in.ID)
	if err != nil {
		return err
	}
	*out = *m
	return nil
}

// MailRaw returns one message as stored. See Visor.MailRaw.
func (r *RPC) MailRaw(in *visorapi.MailMessageRequest, out *[]byte) (err error) {
	defer rpcutil.LogCall(r.log, "MailRaw", in)(nil, &err)
	*out, err = r.visor.MailRaw(in.Folder, in.ID)
	return err
}

// MailSend sends a message. See Visor.MailSend. A send that reached no
// recipient still answers with its per-recipient reasons: net/rpc drops
// the reply of a call that returns an error, and the reasons are the
// useful part, so only a failure with no result is returned as one.
func (r *RPC) MailSend(in *skymail.Outgoing, out *skymail.SendResult) (err error) {
	defer rpcutil.LogCall(r.log, "MailSend", nil)(nil, &err)
	res, err := r.visor.MailSend(*in)
	if res == nil {
		return err
	}
	*out = *res
	return nil
}

// MailDelete removes one message. See Visor.MailDelete.
func (r *RPC) MailDelete(in *visorapi.MailMessageRequest, _ *struct{}) (err error) {
	defer rpcutil.LogCall(r.log, "MailDelete", in)(nil, &err)
	return r.visor.MailDelete(in.Folder, in.ID)
}

// MailSetWhitelist replaces the mailbox whitelist. See Visor.MailSetWhitelist.
func (r *RPC) MailSetWhitelist(in *[]cipher.PubKey, _ *struct{}) (err error) {
	defer rpcutil.LogCall(r.log, "MailSetWhitelist", in)(nil, &err)
	return r.visor.MailSetWhitelist(*in)
}

// MailAttachment decodes one attachment. See Visor.MailAttachment.
func (r *RPC) MailAttachment(in *visorapi.MailAttachmentRequest, out *skymail.AttachmentData) (err error) {
	defer rpcutil.LogCall(r.log, "MailAttachment", in)(nil, &err)
	a, err := r.visor.MailAttachment(in.Folder, in.ID, in.N)
	if err != nil {
		return err
	}
	*out = *a
	return nil
}

// MailSetSettings changes the mailbox settings. See Visor.MailSetSettings.
func (r *RPC) MailSetSettings(in *visorapi.MailSettingsUpdate, _ *struct{}) (err error) {
	defer rpcutil.LogCall(r.log, "MailSetSettings", in)(nil, &err)
	return r.visor.MailSetSettings(*in)
}
