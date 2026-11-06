package interfaces

import (
	"errors"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/go-sql-driver/mysql"
	"gorm.io/gorm"
)

func TestOrganizationSaveErrorDistinguishesConfirmedNameConflict(t *testing.T) {
	for _, item := range []struct {
		err    error
		status int
		code   string
	}{
		{gorm.ErrDuplicatedKey, 409, "conflict"},
		{&mysql.MySQLError{Number: 1062, Message: "sensitive database details"}, 409, "conflict"},
		{errors.New("sensitive database details"), 500, "internal_error"},
	} {
		response := httptest.NewRecorder()
		ctx, _ := gin.CreateTestContext(response)
		writeOrganizationSaveError(ctx, item.err)
		if response.Code != item.status || !strings.Contains(response.Body.String(), `"code":"`+item.code+`"`) || strings.Contains(response.Body.String(), "sensitive") {
			t.Fatalf("%d: %s", response.Code, response.Body.String())
		}
	}
}
