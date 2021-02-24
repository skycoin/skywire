package visor

type TestGateway struct{}

type TestResult struct {
	Text string
}

type TestRequest struct {
	Text string
}

func (r *TestGateway) TestCall(req TestRequest, res *TestResult) error {
	res.Text = "visor response: " + req.Text
	return nil
}
