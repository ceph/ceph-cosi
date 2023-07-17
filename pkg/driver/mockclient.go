package driver

import (
	"net/http"

	"github.com/aws/aws-sdk-go/aws/awserr"
	"github.com/aws/aws-sdk-go/service/s3"
	"github.com/aws/aws-sdk-go/service/s3/s3iface"
)

// MockClient is the mock of the HTTP Client
// It can be used to mock HTTP request/response from the rgw admin ops API
type MockClient struct {
	// MockDo is a type that mock the Do method from the HTTP package
	MockDo MockDoType
}

// MockDoType is a custom type that allows setting the function that our Mock Do func will run instead
type MockDoType func(req *http.Request) (*http.Response, error)

// Do is the mock client's `Do` func
func (m *MockClient) Do(req *http.Request) (*http.Response, error) { return m.MockDo(req) }

type mockS3Client struct {
	s3iface.S3API
}

func (m mockS3Client) CreateBucket(input *s3.CreateBucketInput) (*s3.CreateBucketOutput, error) {
	switch *input.Bucket {
	case "test-bucket":
		return &s3.CreateBucketOutput{}, nil
	case "test-bucket-owned-by-you":
		return nil, awserr.New("BucketAlreadyOwnedByYou", "BucketAlreadyOwnedByYou", nil)
	case "test-bucket-fail-internal":
		return nil, awserr.New("InternalError", "InternalError", nil)
	case "test-bucket-already-exists":
		return nil, awserr.New("BucketAlreadyExists", "BucketAlreadyExists", nil)
	}
	return nil, awserr.New("InvalidBucketName", "InvalidBucketName", nil)
}

func (m mockS3Client) DeleteBucket(input *s3.DeleteBucketInput) (*s3.DeleteBucketOutput, error) {
	switch *input.Bucket {
	case "test-bucket":
		return &s3.DeleteBucketOutput{}, nil
	case "test-bucket-not-empty":
		return nil, awserr.New("BucketNotEmpty", "BucketNotEmpty", nil)
	case "test-bucket-fail-internal":
		return nil, awserr.New("InternalError", "InternalError", nil)
	}
	return nil, awserr.New("NoSuchBucket", "NoSuchBucket", nil)
}

func (m mockS3Client) PutBucketPolicy(input *s3.PutBucketPolicyInput) (*s3.PutBucketPolicyOutput, error) {
	switch *input.Bucket {
	case "test-bucket":
		return &s3.PutBucketPolicyOutput{}, nil
	case "test-bucket-fail-internal":
		return nil, awserr.New("InternalError", "InternalError", nil)
	}
	return nil, awserr.New("NoSuchBucket", "NoSuchBucket", nil)
}

func (m mockS3Client) GetBucketPolicy(input *s3.GetBucketPolicyInput) (*s3.GetBucketPolicyOutput, error) {
	switch *input.Bucket {
	case "test-bucket":
		policy := `{"Version":"2012-10-17","Statement":[{"Sid":"AddPerm","Effect":"Allow","Principal":"*","Action":["s3:GetObject"],"Resource":["arn:aws:s3:::test-bucket/*"]}]}`
		return &s3.GetBucketPolicyOutput{Policy: &policy}, nil
	case "test-bucket-fail-internal":
		return nil, awserr.New("InternalError", "InternalError", nil)
	}
	return nil, awserr.New("NoSuchBucket", "NoSuchBucket", nil)
}
