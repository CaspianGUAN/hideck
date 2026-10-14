package host

import "errors"

func (c *Controller) UpdateCallMedia(deviceID, callID, sdp string) error {
	remote, err := relay(sdp)
	if err != nil {
		return err
	}
	d := c.get(deviceID)
	if d == nil {
		return errors.New("模组直拨会话不存在")
	}
	d.op.Lock()
	defer d.op.Unlock()
	current, err := matchingCall(d, callID)
	if err != nil {
		return err
	}
	if current.bridge == nil {
		return errors.New("模组直拨音频尚未连接")
	}
	return current.bridge.SetRemote(remote)
}
