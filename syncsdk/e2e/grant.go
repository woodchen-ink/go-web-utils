package e2e

// Grant 是为一位成员封装的一个空间密钥版本。
type Grant struct {
	// Version 是空间密钥版本。
	Version int
	// Member 是接收方公钥。
	Member PublicKey
	// Wrapped 是 Keyring.Wrap 产出的 sealed box。
	Wrapped []byte
}

// GrantMember 为新成员封装 keyring 持有的全部版本, 使其能读还没用当前版本重加密的旧文档。
// 旧版本保护的本来就是成员可见的空间文档, 不扩大访问范围。
func (k *Keyring) GrantMember(member PublicKey) ([]Grant, error) {
	var out []Grant
	for _, v := range k.Versions() {
		w, err := k.Wrap(v, member)
		if err != nil {
			return nil, err
		}
		out = append(out, Grant{Version: v, Member: member, Wrapped: w})
	}
	return out, nil
}

// RotateFor 生成新版本空间密钥并封装给每位剩余成员 (调用方自己的公钥也要在列)。
// 被移除者不在列, 他们保留旧版本, 但读不到之后加密的任何内容。
func (k *Keyring) RotateFor(members []PublicKey) (int, []Grant, error) {
	v, err := k.Rotate()
	if err != nil {
		return 0, nil, err
	}
	grants := make([]Grant, 0, len(members))
	for _, m := range members {
		w, err := k.Wrap(v, m)
		if err != nil {
			return 0, nil, err
		}
		grants = append(grants, Grant{Version: v, Member: m, Wrapped: w})
	}
	return v, grants, nil
}
