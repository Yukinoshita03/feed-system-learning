package pagination

import "errors"

var ErrInvalidPage = errors.New("limit 必须为 1 到 100，before_id 必须为非负整数")

// Request 使用递增主键作为游标；第一页不传 before_id，下一页传 next_id。
type Request struct {
	Limit    int  `json:"limit"`
	BeforeID uint `json:"before_id"`
}

func (p Request) PageSize() (int, error) {
	if p.Limit == 0 {
		return 20, nil
	}
	if p.Limit < 1 || p.Limit > 100 {
		return 0, ErrInvalidPage
	}
	return p.Limit, nil
}
