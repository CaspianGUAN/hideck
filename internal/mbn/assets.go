package mbn

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
)

func (s *Service) Assets(ctx context.Context) ([]Asset, error) { return s.store.Assets(ctx) }
func (s *Service) Asset(ctx context.Context, id string) (Asset, []byte, error) {
	assets, err := s.store.Assets(ctx)
	if err != nil {
		return Asset{}, nil, err
	}
	for _, a := range assets {
		if a.ID == id {
			data, err := s.files.Read(a.SHA256)
			return a, data, err
		}
	}
	return Asset{}, nil, ErrNotFound
}
func (s *Service) Upload(ctx context.Context, a Asset, data []byte) (Asset, error) {
	if len(data) == 0 {
		return a, errors.New("不能上传空 MBN")
	}
	if filepath.Base(a.Name) != a.Name || !strings.EqualFold(filepath.Ext(a.Name), ".mbn") {
		return a, errors.New("请选择 .mbn 原始文件")
	}
	// No universal public MBN container validator is available. Never infer
	// device compatibility or trust from the extension or the supplied metadata.
	digest := sha256.Sum256(data)
	a.SHA256 = hex.EncodeToString(digest[:])
	a.ID = a.SHA256
	a.Size = int64(len(data))
	s.mu.Lock()
	defer s.mu.Unlock()
	assets, err := s.store.Assets(ctx)
	if err != nil {
		return a, err
	}
	for _, existing := range assets {
		if existing.SHA256 == a.SHA256 {
			return existing, nil
		}
		if existing.Name == a.Name {
			return a, fmt.Errorf("%w: 同名文件内容不同，请改名后上传", ErrConflict)
		}
	}
	if err := s.files.Put(a.SHA256, data); err != nil {
		return a, err
	}
	if err := s.store.SaveAsset(ctx, a); err != nil {
		return a, errors.Join(err, s.files.Delete(a.SHA256))
	}
	return a, nil
}
func (s *Service) DeleteAsset(ctx context.Context, id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	policies, err := s.store.Policies(ctx)
	if err != nil {
		return err
	}
	for _, p := range policies {
		if p.AssetID == id {
			return fmt.Errorf("%w: 文件被策略引用", ErrConflict)
		}
	}
	jobs, err := s.store.Operations(ctx, "")
	if err != nil {
		return err
	}
	for _, job := range jobs {
		if job.Running() && job.Request.AssetID == id {
			return fmt.Errorf("%w: 文件正被运行中的操作使用", ErrConflict)
		}
	}
	a, data, err := s.Asset(ctx, id)
	if err != nil {
		return err
	}
	if err := s.files.Delete(a.SHA256); err != nil {
		return err
	}
	if err := s.store.DeleteAsset(ctx, id); err != nil {
		return errors.Join(err, s.files.Put(a.SHA256, data))
	}
	return nil
}

func (s *Service) importFile(session Session, op *Operation) error {
	if err := s.validateImport(session.Owner(), op.Request, op.Original); err != nil {
		return err
	}
	a, data, err := s.Asset(s.ctx, op.Request.AssetID)
	if err != nil {
		return err
	}
	digest := sha256.Sum256(data)
	if hex.EncodeToString(digest[:]) != a.SHA256 {
		return errors.New("MBN 文件摘要校验失败")
	}
	if err := s.checkpoint(op, "uploading_to_modem"); err != nil {
		return err
	}
	path := "RAM:hideck-" + op.ID + ".mbn"
	if err := session.Upload(s.ctx, path, data); err != nil {
		return err
	}
	defer func() {
		_, cleanupErr := execute(context.WithoutCancel(s.ctx), session, fmt.Sprintf(`AT+QFDEL="%s"`, path))
		if cleanupErr != nil {
			op.Warning = "临时文件清理失败: " + cleanupErr.Error()
		}
	}()
	if err := s.cancelled(op); err != nil {
		return err
	}
	if err := s.checkpoint(op, "importing"); err != nil {
		return err
	}
	command, err := NamedCommand("Add", path)
	if err != nil {
		return err
	}
	if _, err := execute(s.ctx, session, command); err != nil {
		return err
	}
	op.Current, err = inspect(s.ctx, session)
	if err != nil {
		return err
	}
	newProfiles := 0
	for _, p := range op.Current.Profiles {
		if !hasProfile(op.Original, p.Name) {
			newProfiles++
		}
	}
	if newProfiles == 0 {
		return errors.New("Add 后未发现新增 MBN；不把命令成功当成导入成功")
	}
	return nil
}
