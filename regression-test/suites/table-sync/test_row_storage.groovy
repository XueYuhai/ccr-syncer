// Licensed to the Apache Software Foundation (ASF) under one
// or more contributor license agreements.  See the NOTICE file
// distributed with this work for additional information
// regarding copyright ownership.  The ASF licenses this file
// to you under the Apache License, Version 2.0 (the
// "License"); you may not use this file except in compliance
// with the License.  You may obtain a copy of the License at
//
//   http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing,
// software distributed under the License is distributed on an
// "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY
// KIND, either express or implied.  See the License for the
// specific language governing permissions and limitations
// under the License.
suite("test_row_storage") {

    def tableName = "tbl_row_storage_" + UUID.randomUUID().toString().replace("-", "")
    def syncerAddress = "127.0.0.1:9190"
    def test_num = 0
    def insert_num = 5
    def sync_gap_time = 5000
    String respone

    def checkShowTimesOf = { sqlString, myClosure, times, func = "sql" -> Boolean
        Boolean ret = false
        List<List<Object>> res
        while (times > 0) {
            try {
                if (func == "sql") {
                    res = sql "${sqlString}"
                } else {
                    res = target_sql "${sqlString}"
                }
                if (myClosure.call(res)) {
                    ret = true
                }
            } catch (Exception e) {}

            if (ret) {
                break
            } else if (--times > 0) {
                sleep(sync_gap_time)
            }
        }

        return ret
    }

    def checkSelectRowTimesOf = { sqlString, rowSize, times -> Boolean
        def tmpRes = target_sql "${sqlString}"
        while (tmpRes.size() != rowSize) {
            sleep(sync_gap_time)
            if (--times > 0) {
                tmpRes = target_sql "${sqlString}"
            } else {
                break
            }
        }
        return tmpRes.size() == rowSize
    }

    def checkSelectColTimesOf = { sqlString, colSize, times -> Boolean
        def tmpRes = target_sql "${sqlString}"
        while (tmpRes.size() == 0 || tmpRes[0].size() != colSize) {
            sleep(sync_gap_time)
            if (--times > 0) {
                tmpRes = target_sql "${sqlString}"
            } else {
                break
            }
        }
        return tmpRes.size() > 0 && tmpRes[0].size() == colSize
    }

    def checkRestoreFinishTimesOf = { checkTable, times -> Boolean
        Boolean ret = false
        while (times > 0) {
            def sqlInfo = target_sql "SHOW RESTORE FROM TEST_${context.dbName}"
            for (List<Object> row : sqlInfo) {
                if ((row[10] as String).contains(checkTable)) {
                    ret = (row[4] as String) == "FINISHED"
                }
            }

            if (ret) {
                break
            } else if (--times > 0) {
                sleep(sync_gap_time)
            }
        }

        return ret
    }

    def exist = { res -> Boolean
        return res.size() != 0
    }

    sql "DROP TABLE IF EXISTS ${tableName}"
    sql """
        CREATE TABLE if NOT EXISTS ${tableName}
        (
            `test` INT,
            `id` INT
        )
        ENGINE=OLAP
        UNIQUE KEY(`test`, `id`)
        DISTRIBUTED BY HASH(id) BUCKETS 1
        PROPERTIES (
            "replication_allocation" = "tag.location.default: 1",
            "store_row_column" = "true",
            "binlog.enable" = "true"
        )
    """

    for (int index = 0; index < insert_num; index++) {
        sql """
            INSERT INTO ${tableName} VALUES (${test_num}, ${index})
            """
    }

    httpTest {
        uri "/create_ccr"
        endpoint syncerAddress
        def bodyJson = get_ccr_body "${tableName}"
        body "${bodyJson}"
        op "post"
        result respone
    }

    assertTrue(checkRestoreFinishTimesOf("${tableName}", 30))
    def res = target_sql "SHOW CREATE TABLE TEST_${context.dbName}.${tableName}"
    def rowStorage = false
    for (List<Object> row : res) {
        if ((row[0] as String) == "${tableName}") {
            rowStorage = (row[1] as String).contains("\"store_row_column\" = \"true\"")
            break
        }
    }
    assertTrue(rowStorage)


    logger.info("=== Test 2: add column case ===")
    sql """
        ALTER TABLE ${tableName}
        ADD COLUMN (`cost` VARCHAR(256) DEFAULT "add")
        """
    
    assertTrue(checkShowTimesOf("""
                                SHOW ALTER TABLE COLUMN
                                FROM ${context.dbName}
                                WHERE TableName = "${tableName}" AND State = "FINISHED"
                                """,
                                exist, 30))

    assertTrue(checkSelectColTimesOf("SELECT * FROM ${tableName} WHERE test=${test_num}",
                                      3, 30))


    logger.info("=== Test 3: add a row ===")
    test_num = 3
    sql """
        INSERT INTO ${tableName} VALUES (${test_num}, 0, "addadd")
        """
    assertTrue(checkSelectRowTimesOf("SELECT * FROM ${tableName} WHERE test=${test_num}",
                                   1, 30))
    res = target_sql "SELECT cost FROM TEST_${context.dbName}.${tableName} WHERE test=${test_num}"
    assertTrue((res[0][0] as String) == "addadd")
}