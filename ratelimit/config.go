package ratelimit

type Config struct{
	Capacity float64 // バケツの最大トークン数
	RefillRate float64 // 1秒あたりの補充トークン数
}
