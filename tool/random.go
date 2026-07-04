package tool

import "math/rand/v2"

// RandomNumeric 生成 n 位随机数字字符串
func RandomNumeric(n int) string {
	const digits = "0123456789"
	b := make([]byte, n)
	for i := range b {
		b[i] = digits[rand.IntN(10)]
	}
	return string(b)
}
