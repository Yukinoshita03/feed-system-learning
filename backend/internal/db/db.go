package db

import (
    "feed-system-learning/internal/account"

    "gorm.io/driver/mysql"
    "gorm.io/gorm"
)

func NewDB(dsn string) (*gorm.DB,error){
	database , err := gorm.Open(mysql.Open(dsn),&gorm.Config{})
	if err != nil{
		return nil,err
	}
	return database,nil
}
func AutoMigrate(database *gorm.DB) error{
	return database.AutoMigrate(&account.Account{})
}